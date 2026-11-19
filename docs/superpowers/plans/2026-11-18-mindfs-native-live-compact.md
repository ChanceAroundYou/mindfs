# MindFS 原生上下文活压缩（Live Compact）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有 Claude 会话回环代理里加入可观测、可回滚的上下文活压缩。原计划只交付 Phase A（只测量、不改写）；2026-11-18 用户指令「把 B 全部做完」后追加 Task B1（无损折叠）与 Task B2（账本 + 只读 API + 前端胶囊）。B3（抽取卡片）明确不在本计划内，需另一次设计评审。线上 `mode=on` 仍未启用。

**Architecture:** 沿用 `server/internal/agent/proxy`（MindFS 已有的每会话回环 HTTP 代理）。Phase A 只在 `POST .../v1/messages` 且 body ≥ `min_body_bytes` 时解析请求，统计候选折叠块/字节/估算 token、SSE `usage` 与历史前缀漂移，每个大请求打一行结构化日志；请求与响应字节保持原样。B1 在同一接缝上做字节区间内联改写，把窗口外的旧工具结果折成「指针 + 内容寻址存档」。B2 把每次折叠记进每会话 JSONL 账本，经只读 API 供会话页的「上下文」胶囊展示。`off` 是默认值，零解析开销；`shadow` 只观测；`on` 才折叠。

**Tech Stack:** Go 1.26（`net/http`、`encoding/json`、`httptest`、`-race`）；配置沿用 `config.json → flag → app.StartOptions → agent.Pool → proxy` 管线。

**Spec:** [docs/superpowers/specs/2026-11-18-mindfs-native-live-compact-design.md](../specs/2026-11-18-mindfs-native-live-compact-design.md)

## Global Constraints

- `live_compact.mode` 默认 `off`；未知值归一为 `off`。`on` 在 Phase A 只观测并打一行启动告警。
- 默认值：`keep_rounds=3`、`min_block_tokens=2000`、`min_body_bytes=102400`、`max_drift_messages=8192`。
- 只处理 `POST .../v1/messages`；`count_tokens` 及其他路径一律不解析、不计数。
- `off` / `shadow` 下请求与响应必须与输入 byte-identical；Phase A 不写任何归档、不改任何块。
- 急停：`<config-dir>/context/DISABLED` 存在时按 `off` 处理，stat 缓存 1 秒，即时生效、无需重启。
- 隔离 `ConfigDir` 会话（Router / workflow / 结果改写）只观测、永不折叠，也不进面向用户的节省账本。
- 日志只写尺寸、计数、时间与 8 字符 token 前缀；不写消息正文、工具参数或 session key。
- 上游是 llmux 中转，`usage` 不可信：省量判据用代理自测的 `est_in` / body 字节数，`actual_in` 与缓存计数只做趋势。
- flag 优先于 `config.json`；`min_body_bytes` 与 `max_drift_messages` 只走文件，不占长期 CLI 参数。
- 本阶段不部署、不重启任何节点；开 `shadow` 观测属于单独的部署授权。

## Review Focus

1. **压缩响应体**（客户端带 `Accept-Encoding`、上游回 `Content-Encoding: gzip`）：代理必须跳过 usage 扫描并原样透传压缩字节，不能半解压或改写。测试：`TestLiveCompactCompressedResponseIsForwardedUnscanned`。
2. **不合法 / 缺字段请求体**（`{"messages": not-json`、`{}`）：解析失败必须透传，不能 500、不能卡住转发。测试：`TestLiveCompactShadowToleratesBrokenBody`、`TestParseWireRequestRejectsBodyWithoutMessages`。
3. **普通短请求**（低于 `min_body_bytes`）：不能因为开启观测而增加解析开销或改变行为。测试：`TestLiveCompactSkipsSmallBodies`。
4. **同一 token 并发请求**：计数与哈希历史在 `-race` 下必须一致。测试：`TestLiveCompactConcurrentRequestsSameToken`。
5. **越界路径**（`count_tokens`、辅助端点）：即使 body 很大也不能被观测或改写。测试：`TestLiveCompactIgnoresCountTokensPath`。

---

### Task A1: Token 估算器与 SSE usage 扫描器

**Files:**
- Create: `server/internal/agent/proxy/estimate.go`
- Create: `server/internal/agent/proxy/usage.go`
- Test: `server/internal/agent/proxy/estimate_test.go`
- Test: `server/internal/agent/proxy/usage_test.go`

**Interfaces:**
- Produces: `estimateTokens(string) int`、`estimateByteTokens([]byte) int`、`newUsageScanner() *usageScanner`、`(*usageScanner).Write([]byte) (int, error)`、`(*usageScanner).Totals() usageTotals`、`usageTotals.ContextTokens() int`。

- [x] **Step 1:** 写表驱动测试：ASCII 按 `ceil(bytes/4)`、非 ASCII 每 rune 1 token、空串 0。
- [x] **Step 2:** 写 usage 测试：跨 `Write` 边界拆分的 `data:` 行、非 usage 事件被忽略、超长残行不无限增长。
- [x] **Step 3:** 实现两个估算函数与行扫描器（`maxLine = 1 MiB`；只认 SSE `data:` 行；同时接受 `message.usage` 与顶层 `usage`）。
- [x] **Step 4:** `go test ./server/internal/agent/proxy/` 通过。

### Task A2: Anthropic wire 解析、回合配对与候选分析

**Files:**
- Create: `server/internal/agent/proxy/live_compact_wire.go`
- Test: `server/internal/agent/proxy/live_compact_wire_test.go`

**Interfaces:**
- Produces: `parseWireRequest([]byte) (*wireRequest, error)`、`pairToolRounds([]wireMessage) []*wireToolRound`、`analyzeWire(*wireRequest, keepRounds, minBlockTokens int) wireAnalysis`、`messageHashes(*wireRequest, limit int) []uint64`、`firstDrift(previous, current []uint64) int`、`looksLikeErrorText(string) bool`。
- `foldCandidate` 字段：`MessageIndex/BlockIndex/ToolUseID/ToolName/Tokens/Bytes/ErrorText`；`wireAnalysis` 字段：`Messages/Rounds/ClosedRounds/Candidates/ErrorCandidates/CandidateTokens/CandidateBytes/EstimatedInputTokens`。

- [x] **Step 1:** 测试字符串 content、数组 content、多 `tool_use`、`is_error`、image/document 非文本块、未知块类型。
- [x] **Step 2:** 测试配对：全部 id 有结果才算 closed；部分结果保持 open；open 回合永不入选。
- [x] **Step 3:** 测试候选规则：窗口外、非 error、纯文本、≥ `min_block_tokens`；`is_error=false` 但文本像错误时 `ErrorText=true` 且计入 `ErrorCandidates`。
- [x] **Step 4:** 测试 `firstDrift`：纯追加 -1、中间改写返回首个变化下标、历史缩短返回新长度。
- [x] **Step 5:** 测试无 `messages` / 非法 JSON 返回错误。
- [x] **Step 6:** `go test -race ./server/internal/agent/proxy/` 通过。

### Task A3: 代理接入（off/shadow、路径门控、字节透传）

**Files:**
- Modify: `server/internal/agent/proxy/proxy.go`
- Create: `server/internal/agent/proxy/live_compact.go`
- Test: `server/internal/agent/proxy/live_compact_proxy_test.go`

**Interfaces:**
- Consumes: Task A1 的扫描器与估算器、Task A2 的分析器。
- Produces: `LiveCompactOff/Shadow/On`、`LiveCompactConfig{Mode,KeepRounds,MinBlockTokens,MinBodyBytes,MaxDriftMessages,DisableFile}`、`NewServerWithLiveCompact(LiveCompactConfig) (*Server, error)`、`(*Server).LiveCompactMode() string`、`(*Server).RegisterSessionObserveOnly(token, baseURL, sessionKey)`、`(*Server).SessionFoldAllowed(token) bool`。

- [x] **Step 1:** 测试 `shadow` 转发 byte-identical，且 SSE usage 被记录（`ContextTokens()==1000`）。
- [x] **Step 2:** 测试 `off` 与 `shadow` 对坏 body 均透传。
- [x] **Step 3:** 测试 `count_tokens` 路径不计数；小 body 不解析；并发同 token 计数为 4。
- [x] **Step 4:** 测试压缩响应原样透传（不扫描）。
- [x] **Step 5:** 实现 `handleProxied`：`r.Method == POST && isMessagesPath(remainder)` 才观测；其余路径零额外成本。
- [x] **Step 6:** 实现日志行：`[live-compact] <mode> token=<8> scope=<fold|observe-only> msgs= rounds= closed= keep= epoch= est_in= actual_in= cand_blocks= cand_error_blocks= cand_tokens= cand_bytes= drift= parse_ms=`；`actual_in=-1` 表示未知。
- [x] **Step 7:** `PostCompact` 信号只增加 `compactionEpoch`，不改变观测语义。
- [x] **Step 8:** `go test -race ./server/internal/agent/proxy/` 与 `go build ./...` 通过。

### Task A4: 配置管线与会话范围

**Files:**
- Modify: `server/internal/agent/pool.go`
- Modify: `server/internal/agent/pool_open.go`
- Modify: `server/app/server.go`
- Modify: `cli/cmd/startup.go`
- Modify: `cli/cmd/mindfs.go`
- Modify: `server/cmd/mindfs-server/main.go`
- Test: `cli/cmd/startup_config_test.go`
- Test: `server/internal/agent/pool_compact_proxy_test.go`

**Interfaces:**
- Produces: `agent.PoolOptions{..., LiveCompactDisableFile string}`、`agent.NewPoolWithOptions`、`app.StartOptions{..., LiveCompactDisableFile string}`；`startupConfig.LiveCompact`（`mode/keep_rounds/min_block_tokens/min_body_bytes/max_drift_messages`）。
- 行为：`configDir` 为空的会话 `RegisterSessionWithKey`；非空（隔离）会话 `RegisterSessionObserveOnly`。

- [x] **Step 1:** 测试 `config.json` 的 `live_compact` 全部字段落到 flag 变量，显式 flag 胜出。
- [x] **Step 2:** 测试用户会话 `SessionFoldAllowed == true`、隔离 ConfigDir 会话 `== false` 但仍有代理绑定。
- [x] **Step 3:** 实现三层管线；`app.Start` 在 `LiveCompactDisableFile` 为空时取 `filepath.Join(configDir, "context", "DISABLED")`。
- [x] **Step 4:** `go test ./server/internal/agent/... ./server/app/... ./cli/cmd/...` 与 `go build ./...` 通过。

### Task A5: 急停与失败开放

**Files:**
- Modify: `server/internal/agent/proxy/live_compact.go`
- Test: `server/internal/agent/proxy/live_compact_proxy_test.go`

**Interfaces:**
- Produces: `(*Server).liveCompactDisabled() bool`（1 秒 stat 缓存）；空 `DisableFile` 视为无刹车。

- [x] **Step 1:** 测试：文件不存在 → 观测正常；创建文件 → 观测停止；删除文件 → 观测恢复（测试内重置 stat 缓存，避免 sleep）。
- [x] **Step 2:** 实现 stat 缓存与早退；解析失败只打 `skip reason=parse`，不影响转发。
- [x] **Step 3:** `go test -race ./server/internal/agent/proxy/` 通过。

### Task A6: 真实流量观测与 B1 开闸判据（未执行，需部署授权）

**Files:**
- 无代码改动；读取节点日志与 `config.json`。

**Interfaces:**
- 输入：`live_compact.mode=shadow`（节点本地 `config.json` 或 `-live-compact-mode shadow`）。

- [x] **Step 0:** 本机已 `make install`（新二进制 `v0.5.5-1119-2-g7f1b60ee-dirty`），`~/.config/mindfs/config.json` 设 `live_compact.mode=shadow`；隔离端口（7433 + 临时 XDG 目录）冒烟确认启动日志 `live_compact=shadow`。生产进程仍是旧二进制，等待用户执行 `sudo systemctl restart mindfs`。
- [x] **Step 0b（隔离真实会话冒烟，2026-11-18）：** `go run ./server/internal/agent/proxy/lcsmoke -model ok` 驱动真实 Claude CLI（连续 Read 6 个大文件）跑通 shadow 代理；证据与两个假阳性修复见 spec §12.1。该 harness 是手工工具，不参与产品构建。
- [x] **Step 0c（复核与性能收敛，2026-11-18 晚）：** 用户追问「确定修好了么」后独立复核，发现并修掉三个缺陷：drift 哈希第一轮只修一半（`system.content` 形状翻转）、`est_in`/`actual_in` 跨请求错配（引入 `liveCompactTurn`）、解析 436 ms/8 MB 且队列无全局内存上限（改异步单遍入队 + 96 MiB 预算）。顺带实测手写单遍扫描器仍需 73 ms，**B1 不能内联整包解析**。证据见 spec §12.2。
- [ ] **Step 1:** 经用户授权后部署（`bash scripts/deploy-all.sh` 会提交并推送）；本机 system 单元只能由用户重启，WSL/fn 的 user 单元可由 Agent 重启。
- [ ] **Step 2:** 在至少一个高流量节点设 `mode=shadow` 并重启，跑满 24h。
- [ ] **Step 3:** 从日志统计：`est_in` 分布、`cand_blocks`/`cand_tokens` 分布、`cand_error_blocks` 占比、`drift` 频率、`parse_ms` p95、parse 失败数。
- [ ] **Step 4:** 对照 B1 开闸门槛（见 spec §6）：长会话持续出现候选；单请求预计节省 ≥ total 的 20%（阈值按实测分布定）；parse 失败率为 0；prefix drift 不频繁。
- [ ] **Step 5:** 把分布与结论写回 spec §12 与本文档；未达标则不进 B1。
- [ ] **Step 6（B1 前置，不得跳过）：** spec §12.2 第 3 条要求 B1 设计评审先回答「解析成本怎么进请求路径」——选项是增量/流式改写（只定位替换区间、其余字节透传）或按前缀确定性缓存折叠计划；在该问题有结论前不开 B1。

### Task A7: 定制登记与收尾（提交前）

**Files:**
- Modify: `docs/upstream-customizations.yaml`
- Modify: `docs/plans/README.md`
- Modify: `docs/superpowers/specs/2026-11-18-mindfs-native-live-compact-design.md`

**Interfaces:**
- 分组：`G-MN`，`kind: feature`，登记 Phase A 涉及的全部文件与测试；B1/B2 在同一组内追加。

- [ ] **Step 1:** 在 `groups:` 中新增 `G-MN`，`files` 覆盖：`server/internal/agent/proxy/proxy.go`、`live_compact.go`、`live_compact_worker.go`、`live_compact_turn.go`、`live_compact_wire.go`、`estimate.go`、`usage.go`、`server/internal/agent/pool.go`、`pool_open.go`、`server/app/server.go`、`cli/cmd/mindfs.go`、`cli/cmd/startup.go`、`server/cmd/mindfs-server/main.go`。
- [ ] **Step 2:** `tests` 列出 Task A1–A5 的测试文件；`anchors` 钉住 `[live-compact]` 日志行、`isMessagesPath`、`RegisterSessionObserveOnly`、`DISABLED` 路径、默认值 3/2000/100 KiB/8192、`liveCompactTurn` 配对与 `liveCompactRetainBudget`。
- [ ] **Step 3:** 提交信息标 `Scope: G-MN`；随后 `make check-upstream` 必须绿（该工具只比较已提交文件，提交前红是预期形态）。
- [ ] **Step 4:** 全量门禁：`go test -count=1 ./...`、`go test -race ./server/internal/agent/...`、`make build`。

### Task B1: 无损折叠（用户指令追加，2026-11-18 深夜；已完成，未开 `on`）

**Files:**
- Create: `server/internal/agent/proxy/live_compact_scan.go`、`live_compact_fold.go`、`live_compact_pointer.go`、`live_compact_pointer_cache.go`、`live_compact_archive.go`
- Modify: `server/internal/agent/proxy/proxy.go`（`Server.archive` / `Server.pointerCache`，handler 先折后观测）、`live_compact.go`（配置项与 `[live-compact] fold` 日志行）
- Test: `live_compact_fold_test.go`、`live_compact_fold_proxy_test.go`、`live_compact_pointer_cache_test.go`、`live_compact_archive_test.go`

**Interfaces:**
- `scanWireRequest(body) (*scanRequest, error)` → 只记录区间；`planFold(body, req, cfg, store, cache) foldResult` → 纯函数式的替换计划（存档写盘是唯一副作用）；`verifyFold(before *scanRequest, folded []byte) error` 复用请求路径已有的扫描结果。

- [x] **Step 1（§12.2 第 3 条的前置问题）：** 实测两条路——内联字节区间扫描 2.6 MB 为 1.09 ms / 2400 MB/s / 26 KB 分配；解码全部候选 25 ms。结论：扫描内联、只解码本次新滑出窗口的块。
- [x] **Step 2：** 扫描器与渲染缓存落地；热路径 1.3 ms（37 块全命中），冷路径 ~90 ms（每轮只有一块是冷的）。
- [x] **Step 3：** 存档（内容寻址、0700/0600、temp+rename、去重、7 天 + 2 GiB、限频 sweep、失败记忆化）。
- [x] **Step 4：** 不变量 I1–I7、两套解析器一致性、门禁与急停全部有测试；关键守卫做过变异验证（恒定缓存键、跳过存档存在性/大小检查、放开急停，均能把测试钉红）。
- [x] **Step 5：** 修掉本轮暴露的真缺陷：`foldRequest` 原先只停观测不停改写，急停文件对折叠无效。
- [x] **Step 6：** 门禁：`go test -count=1 ./...` 全绿、`go test -race ./server/internal/agent/proxy/` 绿、`go vet` 干净、`make build` 成功。
- [ ] **Step 7（未做）：** 真实流量灰度 —— 先 WSL 或 fn 开 `on` 观察 24h，再本机（需用户重启），pc-win 最后。

### Task B2: 账本 + 只读 API + 前端上下文 capsule（用户指令「把 B 全部做完」；已完成，未开 `on`）

**Files:**
- Create: `server/internal/agent/proxy/live_compact_ledger.go`、`server/internal/agent/pool_context_saving.go`、`server/internal/api/http_session_context_saving.go`、`server/internal/api/usecase/session_context_saving.go`、`web/src/services/agent/contextSaving.ts`、`web/src/components/agent/useContextSaving.ts`、`web/src/components/agent/ContextSavingIndicator.tsx`
- Modify: `server/internal/agent/pool.go`（`LiveCompactArchiveDir`）、`server/app/server.go`（默认 `<config-dir>/context` 并下传）、`server/internal/api/http_routes.go`、`web/src/components/action/ComposerTopChips.tsx`、两份 locale
- Test: `live_compact_ledger_test.go`、`pool_context_saving_test.go`、`http_session_context_saving_test.go`、`web/tests/context-saving-chip.test.mjs`

**Interfaces:**
- `proxy.ContextSaving{Latest, Requests, MaxSavedBytes, Recent}`；`(*Server).SessionContextSaving(sessionKey string) ContextSaving`；`(*Pool).SessionContextSaving(...)`；`(*Service).GetSessionContextSaving(ctx, GetSessionContextSavingInput) (proxy.ContextSaving, error)`；`GET /api/sessions/{key}/context-saving`。

- [x] **Step 1：** 账本落地 —— 每会话一个 `<config-dir>/context/ledger/<sha256(key)[:8]>.jsonl`，一次折叠一行，只记大小与计数；4 MiB 用自身尾部 2000 行替换。
- [x] **Step 2：** 修掉启动级缺陷 —— `LiveCompactArchiveDir` 从未接进启动路径，生产 `mode=on` 折不了任何东西；现在默认 `<config-dir>/context` 并沿 `PoolOptions` 下传到代理。
- [x] **Step 3：** 只读链路 —— 代理只读方法 → 池转发（无代理返回零值）→ 用例（`InferAgentFromSession` + `agentPoolSessionKey` 解析池级 key）→ chi 路由。
- [x] **Step 4：** 前端 —— 服务（`appURL(..., nodeId)`）、hook（挂载/切换/`pending` true→false 各拉一次、代际守卫、无轮询）、胶囊（无节省 `return null`、portal 浮层）、`ComposerTopChips` 接线、中英各 12 条文案。
- [x] **Step 5：** 测试 —— 路由存在/空 key、池级 key 不串账本、无代理不 panic、archive dir 真接进代理；前端结构测试 8 条（路径一致、节点与 root、显隐、刷新时机、切换清空、文案成对）并做变异验证（三处变异各钉红一条对应用例）。
- [x] **Step 6：** 门禁：`go test -count=1 ./...` 全绿（28 包 ok）、web 233 文件 / 659 用例通过、`tsc -b --noEmit` 干净、`make build` 成功。
- [x] **Step 6b（2026-11-19 收尾）：** 补 `usecase/session_context_saving_test.go`（真实池 + 真实代理写账本，钉住「先解析 agent 再查池级 key」；变异验证过），`lcsmoke -mode/账本读回`；真实 CLI `-mode on` 冒烟 8 次折叠、`latest={saved=72227,blocks=5}`；记录 `on` 模式 `drift` 自噪声（spec §12.4）。全量门禁与 `make check-upstream`（1915 delta / 307 组）复跑通过。
- [x] **Step 6c（2026-11-19 隔离实例 A/B 对比）：** 同一工作负载（**一条会话**、6 次顺序大文件 Read、每次 93,755 B）、两个隔离实例（7441 `shadow` / 7442 `on`，`XDG_CONFIG_HOME=${root}/config`，线上 7331 未碰），经 MindFS 真实 WS 回环（`session.message` → CLI → 代理 → 上游）跑完。结果：`on` 侧第 2/3/4/5 轮 `est_in` 相对 `shadow` 缓降 0.1% → 26.0% → 41.1% → 51.5%，第 1 轮完全一致（42632 vs 42585，+0.1% 是请求里的随机 uuid）；账本 `requests=5`、`max_saved_bytes=378,240`、折叠 4 块 / 93,756 tokens_est；末轮 `req_bytes=651,731 → folded_bytes=273,491`（-58%）；存档 4 个 93,755 B 文件（0700/0600，内容可读回原文）。正确性：两侧模型都完成了任务；**`shadow` 在 7 轮时撞上 200k 窗口并自动压缩失败（`Prompt is too long`），`on` 未触发压缩**，墙钟 521.4 s vs 301.8 s。未开 `on` 的线上节点、未提交。
- [x] **Step 6d（2026-11-19 缓存代价复测）：** 增加第三臂 `control`（7443，`mode=on` + `keep_rounds=20`，6 轮内零可折块），把「`on` 代码路径」与「实际改写」分开。结论：`on` 自第 4 个请求起 `cache_read` 钉死 ~17.86k，`input_tokens` 37.3k → 75.5k，每轮全价重算折叠后历史；第 2–5 请求按 1x/0.1x 折算 shadow ≈179.2k、control ≈179.0k、on ≈237.4k —— **原始 token −24.7%、成本 +32.5%**；control 与 shadow 逐轮重合，证明是改写而非代码路径导致。**这违反 §9 优先级 2，`mode=on` 开闸前必须解决**；实测与方法见 spec §12.5（含补 `cache_control` 断点等候选方向，需另一次评审）。
- [ ] **Step 7（未做，且被 Step 6d 阻塞）：** 与 B1 Step 7 合并 —— 灰度顺序仍是 WSL/fn → 本机（用户重启）→ pc-win，但先要解决缓存代价。

### Task B4: 低频大折 + 单调边界（2026-11-19 用户批准；Phase A 先做）

**Files:**
- Modify: `server/internal/agent/proxy/live_compact.go`（新增 5 个配置项与默认值）
- Modify: `server/internal/agent/proxy/live_compact_fold.go`（提交集、rail 门禁、预算选择）
- Modify: `server/internal/agent/proxy/proxy.go`（`foldRequest` 前算 est/growth；`gate` 传进观测）
- Create: `server/internal/agent/proxy/live_compact_commits.go`（会话级已提交集合）
- Modify: `server/internal/agent/proxy/live_compact_worker.go` / `live_compact.go`（`gate=` 日志字段）
- Modify: `cli/cmd/startup.go`、`cli/cmd/mindfs.go`、`server/cmd/mindfs-server/main.go`、`server/app/*`、`server/internal/agent/pool.go`（config.json → PoolOptions → proxy 同管线，只走文件）
- Test: `live_compact_commits_test.go`、`live_compact_fold_gate_test.go`、`live_compact_fold_proxy_test.go`（补 Gate 用例）

**Interfaces:**
- `foldCommits{has(key) bool; add(key)}`；`foldBudget{commitNew bool; minNewBytes, maxNewBytes int}`；`planFoldCommit(body, req, cfg, store, cache, commits, budget) foldResult`（`planFold` 保留为「全部提交」的兼容包装，既有测试不动）。
- `foldResult` 新增 `stickyBlocks` / `newBlocks`。

- [x] **Step 1（RED）：** 写 I8 用例——同一会话第二次请求时 `committed` 命中的块必须折回，即使 rail 门禁关闭；再写「pointerCache 清空后仍折回」（模拟 4096 条重置）。
- [x] **Step 2（RED）：** 写 I9 用例——未过 rail 时，连续两次请求的折叠区逐字节一致；过 rail 后新提交，边界只右移。
- [x] **Step 3（RED）：** 写预算用例——`min_fold_bytes` 拦住小刀、`max_fold_bytes` 截断、`large_block_tokens` 在未过 rail 时单独放行、收尾到 `target_tokens` 停止。
- [x] **Step 4（GREEN）：** 实现 `live_compact_commits.go` 与 `planFoldCommit`；`foldRequest` 计算 est/growth、判定 gate、把 `gate` 交给观测。
- [x] **Step 5（GREEN）：** 配置与启动管线（只走 `config.json`）；`Normalized()` 默认值 130000/100000/32768/262144/32000 与 `target < rail` 校验。
- [x] **Step 6：** 变异验证——去掉提交集、去掉 rail 门禁、去掉 min_fold 守卫，各自应钉红对应用例。
- [x] **Step 7：** 门禁：`go test -count=1 ./...`、`go test -race ./server/internal/agent/proxy/`、`make build`。
- [x] **Step 8（隔离实例三臂）：** 复用 `/tmp/lcbench` 的 Read 负载（本轮 8×93,755 B），验收 §12.6 四条。**第一次 FAIL**（2.26×），见下方「Step 8 结果」。
- [x] **Step 8b（2026-11-19，追加）：** 按 §12.7 复盘（缓存语义 + 折深定律）改默认值，再补 `keep_tail_tokens`；**第二次三臂 PASS**，见下方「Step 8 复测第二次」。
- [ ] **Step 9（未做，单独授权）：** WSL/fn `shadow` 24h 预演；准入 ≥3 次 `gate=fold` 且平均 `est_after/est_before < 0.8`。

**B4 实施结果（2026-11-19，Step 1–8 完成，Step 8 验收 FAIL；Step 9 未做）：**

- **提交集**：`live_compact_commits.go`，进程内、按会话、永不淘汰（`pointerCache` 在 4096 条整体重置，不能当承诺记录）。`planFoldCommit` 两遍：先无条件折已提交块（I8），再在门禁放行时按最旧优先提交新块。
- **门禁**：`live_compact_fold_gate.go` 的 `decideFoldBudget` —— `est<=0` 或 rail 非正 → `unmeasured`（保持旧「全部提交」语义，兼容负 rail 的存量用例）；`large_block` 或 `est - sticky + growth > rail` → `fold`；否则 `below-rail`（只折已提交块，I9）。
- **预算**：`min_fold_bytes` 不足则整组丢弃并记 `gate=no-backlog`（不回累积）；`max_fold_bytes` 截断本次；收尾依据 `target_tokens` 停止。`planFold` 保留为全提交包装，B1 既有用例零改动。
- **配置**：`rail_tokens` / `target_tokens` / `min_fold_bytes` / `max_fold_bytes` / `large_block_tokens` 只走 `config.json`，经 `startupConfig → startupFlags → server.Options → PoolOptions → proxy.LiveCompactConfig`；默认 130000/100000/32768/0/32000，`target >= rail` 时拉到 `rail*3/4`，负 rail 关闸。`TestLiveCompactRailConfigFromFile` 钉住文件值落地。
- **修掉的缺陷**：`proxy.go` 里 `body, _, gate := s.foldRequest(...)` 在 `if` 内重新声明 `body`（因为 `gate` 是新变量），改写的 body 被静默丢弃——`TestProxyFoldOnRewritesOldToolResults` 抓到的正是这个。
- **测试**：`live_compact_fold_gate_test.go` 11 条（I8 命中/缓存重置、I9 逐字节稳定、min/max/large-block 预算、默认值归一化、经真实 handler 的跨轮 I8）。既有用例里依赖「默认全折」的（`pool_context_saving_test.go`、`usecase/session_context_saving_test.go`）显式 `RailTokens: -1`。
- **长读豁免（2026-11-19 补）**：`keep_rounds` 原来先把最近轮整片挡在候选之外，`large_block` 只能对已经出窗的块生效——最新一轮的巨型读取永远折不了，恰好是「这一轮就会撞窗口」的那种请求。现在 `collectFoldCandidates` 用 `roundHasOversizedResult` 把所有含 ≥ `large_block_tokens` 可折结果的轮**全部**豁免出 keep 窗口，而不只是最新一轮：承诺集按原始转录重算（dsh 是把会话本体改写掉，所以它只需要 `longSpan` 指最新长轮），只豁免最新一轮会让先前已提交的那一轮在新长轮出现时滑回窗口内、指针无声展开，违反 I8。普通轮仍由 `keep_rounds` 保护（门禁放行也不折最新普通轮）。`TestNewestRoundWithOversizedResultIsNotProtected` / `TestNewestNormalRoundStaysProtected` / `TestCarvedOutCommitSurvivesNewerOversizedRound` 三条钉住；变异：去掉豁免 → 第 1、3 条红；退回「只豁免最新一轮」→ 第 3 条红；无视 keep 窗口 → 第 2、3 条红。
- **shadow 预演（2026-11-19 补，§12.6「观测」）**：shadow 原来只挂观测、`gate` 恒为 `unmeasured`，Step 9 的准入判据（`gate=fold` 次数、`est_after/est_before`）无从统计。现在 shadow（以及 `on` 但无存档）在请求路径执行 `previewFold`：复用同一份 `collectFoldCandidates` + `decideFoldBudget` 算出决策与「本请求会折掉多少 token」，写进 `[live-compact] <mode>` 日志的 `gate=` / `would_fold_tokens=`；`fold` 行补 `gate=`。预演不渲染指针、不碰 `pointerCache`、不写存档、不改 body，但会把「本请求会提交的块」记进同一个 `foldCommits` —— 否则每轮都像没折过、门禁每轮都触发，预演的不是 `on` 实际执行的政策。`planFoldCommit` 的 gate 也回填 `would_fold_tokens`（`on` 下等于本次实际折掉的）。测试 `live_compact_shadow_preview_test.go` 4 条（预演不改 body / 不写存档、提交后低于 rail 仍计入 sticky、巨型块低于 rail 也触发、日志字段）；变异：关掉 shadow 预演 → 前 3 条红；预演不写提交集 → sticky 那条红。
- **配套**：`server/cmd/mindfs-server/main.go`（独立 server 二进制的重复 config 解析）补上 5 个新键，与 `cli/cmd` 同管线；只读改文件，不加 CLI flag。
- **变异验证**：去掉提交集 → 3 条 I8 红；关掉 rail 比较 → 同上；去掉 `min_fold_bytes` / `max_fold_bytes` / `large_block` 分支 → 各钉红对应用例；长读豁免三处变异见上条；还原后全绿。
- **门禁**：`go test -count=1 ./...` 除 `server/internal/api` 的 `TestWorkflowNodeAdapterSessionReusePolicies` 外全绿（28 包 ok，含 `server/internal/kanban`）；`go test -race ./server/internal/agent/proxy/` 绿、`make build` 成功。该 api 失败属同工作区另一条会话的在飞改动：未跟踪的 `server/internal/kanban/workflow_session_name.go` + 修改中的 `appcontext_workflow.go` 把会话名改成「任务名 / #编号 · 节点」，测试仍期望旧名，不在本任务范围。

**Step 8 结果（2026-11-19）：FAIL，未在任何节点开 `mode=on`。**

三臂隔离实例（7441 `shadow` / 7442 `control`=`on` + rail 关闭（1e9）/ 7443 `on` 默认值；线上 7331 未触碰，二进制 `v0.5.5-1120-11-g1b9aebed-dirty`），负载为 8×93,755 B 顺序 Read + 一次 `wc` 校验（比 §12.6 写的 6 次多两个文件，其余同构）。

| 臂 | 提交新块的折叠 | 首次自动压缩 | max est_in | 末轮 est_in | 折算成本 | 墙钟 |
|---|---|---|---|---|---|---|
| `shadow` | 0 | 第 6 个请求（epoch 0→1） | 123,065 | 121,643 | 327,876 | 651 s |
| `control` | 0 | 第 6 个请求 | 123,124 | 122,336 | 369,205 | 718 s |
| `on` | 3 次（日志 7 行，含 sticky 重渲染） | 第 15 个请求（epoch 0→1） | 114,490 | 58,665 | 740,634 | 1,069 s |

折算成本 = `input×1 + cache_creation×1.25 + cache_read×0.1`（§12.5 同法）。`control` 与 `shadow` 逐请求重合（差异来自多一轮），说明 `on` 代码路径在「不折」时本身不加成本。

四条验收：

1. **折叠事件 ≤2：FAIL** —— 3 次提交新块（最终折叠区 5 块 / 74,511 tokens_est / 298,042 B）。
2. **`on` 不撞 `Prompt is too long`、`shadow` 撞：未复现** —— 三臂都没有该错误；实际失败形态是 CLI 自动压缩（epoch 0→1），`shadow` 在第 6 个请求、`on` 在第 15 个请求。折叠确实推迟了压缩（`shadow` 读到第 5 个文件后压缩，`on` 读到第 8 个文件后压缩），但没能避免。
3. **成本 ≤ `shadow × 1.10`：FAIL** —— 740,634 / 327,876 = **2.26×**。总量对比被 agent 自身行为差异放大：`on` 臂改用 offset/limit 分块读（16 次 Read vs 8 次；第一次重复读发生在首个折叠之前，不是折叠导致的），只看总量会高估折叠的代价。但折叠自身的代价可以单独量出来：每次提交新块后的下一个请求 `cache_read` 掉到 9,362、`input` 涨到 ~121k（整段历史全价重算），同臂普通请求约 31k；三次事件合计 ≈ **+275k**。即使三臂读法完全一致，`on` 也约 1.8× `shadow`。
4. **末轮 `est_in` ≤ `shadow × 0.8`：数值 PASS（0.48），但不是证据** —— 两边末轮都在自动压缩之后，比较的是压缩后的上下文，不是策略效果。

另外两点：

- **rail 的量纲不对**：实测 `actual_in ≈ est_in × 1.33`（114,490 → 152,382；llmux 的 usage 已知会放大，绝对值不可信，但方向一致）。按这个比例，rail 130k `est` ≈ 174k actual，已经越过 CLI 的自动压缩阈值（约窗口 −33k ≈ 167k）——「过 rail 才折」等于「已经晚了」。
- **`max_fold_bytes` 默认值与已批准 spec 不一致**：spec §12.6 表与计划正文都写 262,144 B，代码 `Normalized()` 给 0（不限），`TestLiveCompactNormalizesRailDefaults` 也钉 0。本轮 `on` 有一次事件折了 3 块 / 298,042 B，超过了文档写的单次上限。

结论：B4 默认值不满足 §12.6；在修正 rail 量纲与折叠预算前不进入 Step 9，也不在任何节点开 `mode=on`。复测用的三臂脚本与日志在 `/tmp/lcbench/`（`run_arm_b4.sh`、`analyze_b4.py`、`logs/`），属一次性实验产物，未入库。

**Step 8 后口径修正（2026-11-19，未复测）：**

- **rail 改按上游 token 计量**：会话保存一对校准样本（上一请求的上游 `ContextTokens()` 与代理为它转发的估算），`foldContext.actual / forwardEst` 把 growth 与 `target_tokens` 一起折算；样本缺失（首请求 / usage 未扫到 / 压缩流）退化为 1:1，即旧的纯估算行为；样本比截断在 0.5x–3x。上游样本只在 `Seen && ContextTokens()>0` 时写入，用 0 覆盖好样本会让闸门永久退化。观测行新增 `gate_projected=`。
- **`max_fold_bytes` 零值语义改为「默认 262144」**，与本节表格和 spec §12.6 一致；此前 `Normalized()` 给 0（不限），一次事件可折掉整段历史。
- **shadow 预演同步扣减 sticky**：shadow 转发的是未折 body，它测到的上游上下文仍含 mode=on 会折掉的块，按上游单位扣掉后预演才与真实 planner 同口径。
- 测试 6 条（`TestRailUsesUpstreamTokenScale` / `TestActualScaleClampsOutliers` / `TestRailFallsBackToEstimator` / `TestFoldStopsAtTargetInUpstreamUnits` / `TestSessionCalibrationRoundTrip` / `TestShadowPreviewSubtractsStickyInUpstreamUnits`），变异 5 处各自钉红；`Normalized()` 默认值用例改为 262144。
- **注意方向**：修正后 rail 会更早触发（130k 现在是 130k 上游 token，而原来的 130k est ≈ 174k 上游），即**折得更频繁**。在每次事件都近乎全量缓存重建的前提下，这与成本目标相反 —— 复测前应先定「事件间隔」策略，见 B5 的取舍。

**Step 8 复盘 spike（2026-11-19，离线 wire 级，结论写进 spec §12.7）**

在调参之前先测清缓存语义。脚本在 `/tmp/lcspike/`（一次性，未入库），每个请求加随机 UUID 前缀隔离跨臂污染（此前两轮测量得出「`cache_control` 有效」的错误结论，正是跨臂污染造成的；单个臂的第一个请求命中了上一个臂已写下的缓存）。

1. **命中 = 最长公共前缀，`cache_control` 完全无效**。同一份 8 轮转录改最后一轮 → `cache_read` 133,632→117,248；改中间一轮 → 65,024；改第一条 → 0。Q1（全程无 `cache_control`）与 Q2（只在 `system` 上带一个）返回**完全相同**的 `hit=70,144` / `bytes=124,861`。**→ B5 的前提被否决**，该方向只在切到 Anthropic 直连时才有意义。
2. **收益由折深决定，与折频无关**。7/8 块（深折）：324,675 → **287,879（−11.3%）**；4/8 块：361,878（+11.5%）；2/8 块：426,379（+31.0%）。另一组 80 KB/轮：折一次浅的 +16%，**每轮重折 2.54×**。
3. **代价定律**：`回收轮数 ≈ 10 × F / (S − F)`（F 折后、S 折前）。B4 原默认 `130k→100k` 相对 200k 窗口只砍约 15%，回收期约 33 轮，而门禁 3 轮左右就触发一次 —— 算术上必亏，与 Step 8 的 2.26× 一致。
4. **dsh 赢在形状不在阈值**：它把切点写回会话本体（`surfaceOp:{op:"replace"}`），前缀此后逐字节不变。我们作为线上代理只能靠 `foldCommits`（切一次就不动）逼近同样的稳定性。dsh 的 20k/650k 是它的窗口与工具形态，不可照搬；可搬的是「触发晚、一刀深、之后不再切」。

**默认值修订（spec §12.7(d)，已落地）**：`rail_tokens` 130,000 → **160,000**（按窗口比例）；`target_tokens` 100,000 → **40,000**（实际约束通常落到尾部预算）；`min_fold_bytes` 32,768 → **4,096**（dsh `chunkMin` 是 1k token；门槛从「够不够一次断点」变成「折完是不是真的更小」）；`max_fold_bytes` 262,144 **语义改为「单块上限」**，一次事件最多排空 8 块（`foldMaxChunksPerEvent`，对应 dsh 的 8 遍 drain）——保留原来的「事件总上限」会让一次只折两块，那正是 §12.7(b) 实测比不折更贵的浅折。`TestFoldEventRespectsMaxBytes` 改成钉「一块一块排空、到 8 块停」，`TestLiveCompactNormalizesRailDefaults` 钉新默认值。

**Step 8 复测第一次：仍然 FAIL（2026-11-19，只改阈值）**

先只改默认值（`rail=160000 / target=40000 / min_fold=4096`）重跑三臂，`on` 仍是 **2.0× shadow**（713,425 / 353,809），6 次折叠事件、5 次提交新块，日志形态很明确：

```
msgs=8  rounds=3  est_in=96089  actual_in=128064 → fold blocks=1 new_blocks=1
msgs=10 rounds=4  est_in=96610  actual_in=128728 → fold blocks=2 new_blocks=1
msgs=12 rounds=5  ...                            → fold blocks=3 new_blocks=1   （共 5 次）
```

**每轮只折掉一块**。根因不在阈值：`keep_rounds=3` 保护的尾部按轮数算，这个负载每轮 ≈ 25k token，3 轮 = 75k 尾部；rail 在 160k 触发时只有 1 轮滑出保护区，于是每次事件都只够折一块 —— 正是 §12.7(b) 判定为「比不折更贵」的浅折。dsh 对应的是 `keepRoundTokens=32000`：尾部**按 token 封顶**，不只是按轮数。

**补 `keep_tail_tokens`（dsh `keepRoundTokens` 的对应物）**：从最旧的受保护轮开始按估算 token 累计，超过预算就把它移出保护区（最新一轮永不折，巨型块仍由 `large_block_tokens` 单独放行）。默认 32000，只走 `config.json`（无 CLI flag），与折叠预算同管线（`startupConfig → startupFlags → server.Options → PoolOptions → proxy`）。`collectFoldCandidates` 里 `protectedFrom` 现在是「轮数」与「字节预算」两个约束的**较大者**。`TestKeepTailTokensShrinksProtectedWindow` 三条（轮数约束、token 约束、最新轮永不折）；变异：关掉预算 → 第二条红。`TestCarvedOutCommitSurvivesNewerOversizedRound` 显式把预算设成 1<<30，因为它钉的是 keep 窗口滑动，不该被字节预算掩盖。

**Step 8 复测第二次：PASS（2026-11-19，`keep_tail_tokens=32000`）**

同样负载（三臂都是 8×93,755 B 顺序 Read；`run_arm_b4.sh` 每次 `rm -rf` 自己的项目目录，三臂逐字节同构）：

| 臂 | 提交新块的折叠 | 自动压缩 | max `actual_in` | 末轮 `est_in` | 折算成本 | 墙钟 |
|---|---|---|---|---|---|---|
| `shadow` | 0 | 第 6 请求压缩，随后 **`Prompt is too long`** | 203,075 | 96,174 | 353,809 | 573 s |
| `control`（`on`，rail 关） | 0 | 第 6 请求压缩成功 | 164,872 | 122,191 | 396,604 | 570 s |
| `on`（新默认） | **2 次**（各 3 块） | **从未** | **129,994** | **73,900** | **350,096** | 485 s |

四条验收：① 折叠事件 2 次 ≤2 **PASS**；② `on` 不撞 `Prompt is too long`、`shadow` 撞 **PASS**（shadow 的压缩 summarization 直接失败）；③ 成本 350,096 / 353,809 = **0.99×** ≤1.10 **PASS**（相对 `control` 是 −11.7%）；④ 末轮 `est_in` 73,900 / 96,174 = **0.77** ≤0.8 **PASS**。形态也对：两次事件各折 3 块，中间 3 个请求 `gate=below-rail` 且 sticky 重放同样的折叠区（I8/I9 在真实流量上成立），`actual_in` 在 128k→57k、130k→59k 之间锯一次再长回去。

**仍未证明的事**：`cache_creation` 在本上游恒为 0，折算仍按 §12.5 的相对口径；`shadow` 臂的 `Prompt is too long` 是压缩失败而非窗口硬拒，所以第 2 条验收按「是否撞上」记 PASS 有解释空间（`on` 全程 130k 以下，没有到过阈值，这是更强的证据）。n=1 负载、单机 llmux 别名模型。Step 9 的 24h `shadow` 预演仍未做。

**Step 8 复测第三次：低 rail 对照（2026-11-19，用户问「为什么不参考 20k / 50k」）**

同负载另跑 `rail50`（rail 50k / target 40k）与 `rail20`（rail 20k / target 15k），其余默认值不变：

| 臂 | 新块折叠 | 峰值 est / actual | 折算成本 | 墙钟 |
|---|---|---|---|---|
| `control` | 0 | 123,053 / 164,872 | 396,604 | 570 s |
| `on`（默认） | 2 | 97,560 / 129,994 | 350,096 | 485 s |
| `rail50` | 7（每请求一块） | 49,562 / 60,909 | **347,897** | 591 s |
| `rail20` | 7（每请求一块） | 49,285 / 60,738 | 363,450 | 553 s |

**低 rail 不比默认值贵**（50k −0.6%，20k +3.8%），峰值降到 60k。第一次复测的 2.0× 根因是「75k 尾部 +
target 100k 让每次事件后仍有 ~121k 要按 1x 重算」，不是频率；`keep_tail_tokens` 落地后每次事件体量
~39k，频率的代价消失。机制：低 rail 每轮折掉的正是本轮刚读入、本来就要按 1x 付的那块，不额外花钱；
代价是折叠区不再命中缓存（`cache_read` 恒 ~18k 静态头）。20k 低于「系统头 + 最新一轮」的物理地板
（~57k actual），与 50k 逐行同形。

**默认值暂不改**：低 rail 每轮改写请求体（7/8 vs 2/8），且「读之间夹大量对话/思考」的负载会退化成
spike 的每轮重折形状——这条负载没覆盖。下一步：100k 臂 + 交错负载，然后才决定 rail（§12.7(e)）。

**Step 8 复测第四次：统一读法后重跑（2026-11-19，用户问「target 是不是定得太高，50 至少压到 20」）**

第三次的七条臂里 agent 读法不一致（有的带 `offset`/`limit` 分块读、有的重复读同一文件），成本不可
横比。把提示词钉成「每个文件只调一次 Read、不许带 offset/limit、不许重读」后重跑七臂，transcript
逐个核对全部是 8 次整文件读。折算成本与上下文峰值（`input + cache_read` 的逐请求最大值）：

| 组 | rail / target | 折算成本 | 均值 | 上下文峰值 |
|---|---|---|---|---|
| 默认 | 160k / 40k | 340,190 / 367,914 / 357,041 | 355,048 | 129.6 / 129.7 / 129.7k |
| 100k | 100k / 20k | 339,933 / 380,111 | 359,822 | 93.2 / 95.2k |
| 50k | 50k / 20k | 394,534 / 389,015 | 391,775 | 58.8 / 61.6k |

**结论（修正第三次）**：50k 不比默认值便宜，两次都在默认三次之上（约 +10%）；100k 与默认打平。
第三次的 rail50 347,897 是读法混杂的产物。机制：rail50 在第一轮滑出保护窗口时就触发，此后每轮改写
请求体，除静态头（~9.4k）外前缀不再命中缓存，每次请求按 1x 全价；默认值前 3 个请求能吃到 54–90k 的
`cache_read`（0.1x）。

**target 在 rail 50 上不构成约束**：t40（第三次）、t20（本次两条）、t2（另跑一条）三条独立臂的折叠
序列逐块相同——7 次事件、每次 1 块、每块 24,837 est。折深 = rail 触发时已积压的可折轮数；rail50 每拍
只积压 1 块，target 再低也无块可折（break 条件只提前结束 drain，不能制造 backlog）。全部折完后发送体
仍停在 47.9–51.9k est / 59–63k actual（`gate=no-backlog`、`would_fold_tokens=173,859` 已是全部），
所以 20k 与 target 无关，只能靠折掉模型正在读的那一轮（B3）——dsh 也不做。

**默认值定为 100k / 20k（2026-11-20 用户批准）**：成本与旧默认打平、峰值 130k → 95k；50k 要付 ~10%，
不采纳。50k 的「每轮重折」形状仍未被覆盖 —— 下一步（需单独授权）：「小回合与大读交错」的负载复测
100k vs 旧默认。`mode=on` 仍保持不开。改动提交 `9e29cdd2`，随 `v0.5.5-1121-31-g9e29cdd2` 部署四端
（2026-11-20），本机 system 单元待用户重启。

### Task B5（已否决，2026-11-19）：折叠边界补 `cache_control`

原条件是「B4 Step 8 无法把折算成本压到 `shadow × 1.10` 以内时启动」。前置 spike 已给出答案：
本链路（llmux → 上游）按**最长公共前缀**自动缓存，`cache_control` 被忽略（spec §12.7(a)，Q1/Q2 逐字相同）。
补断点不可能让折叠后的前缀命中，因此**不启动**；若将来切到 Anthropic 直连，本节作为前置 spike 清单
（`cache_control` 能否挂在 `tool_result` 上、第二个请求的 `cache_read` 是否覆盖折叠区）仍然有效。

## 明确不在本计划内

- B3 抽取卡片。
- 任何 `mode=on` 的线上启用（B1 代码已就绪，灰度顺序见 spec §6）。
- （B1 折叠实现原不在本计划内，2026-11-18 由用户指令「把 B 全部做完」追加为 Task B1。）
