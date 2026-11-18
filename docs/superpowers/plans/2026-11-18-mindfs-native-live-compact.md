# MindFS 原生上下文活压缩（Live Compact）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有 Claude 会话回环代理里加入可观测、可回滚的上下文活压缩；本计划只交付 Phase A（只测量、不改写），并用真实流量数据决定 B1 是否开闸。

**Architecture:** 沿用 `server/internal/agent/proxy`（MindFS 已有的每会话回环 HTTP 代理）。Phase A 只在 `POST .../v1/messages` 且 body ≥ `min_body_bytes` 时解析请求，统计候选折叠块/字节/估算 token、SSE `usage` 与历史前缀漂移，每个大请求打一行结构化日志；请求与响应字节保持原样。`off` 是默认值，零解析开销；`shadow` 只观测；`on` 在本构建中也会退化为观测，直到 B1 落地。

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
- 分组：`G-MG`，`kind: feature`，登记 Phase A 涉及的全部文件与测试；B1/B2 在同一组内追加。

- [ ] **Step 1:** 在 `groups:` 中新增 `G-MG`，`files` 覆盖：`server/internal/agent/proxy/proxy.go`、`live_compact.go`、`live_compact_worker.go`、`live_compact_turn.go`、`live_compact_wire.go`、`estimate.go`、`usage.go`、`server/internal/agent/pool.go`、`pool_open.go`、`server/app/server.go`、`cli/cmd/mindfs.go`、`cli/cmd/startup.go`、`server/cmd/mindfs-server/main.go`。
- [ ] **Step 2:** `tests` 列出 Task A1–A5 的测试文件；`anchors` 钉住 `[live-compact]` 日志行、`isMessagesPath`、`RegisterSessionObserveOnly`、`DISABLED` 路径、默认值 3/2000/100 KiB/8192、`liveCompactTurn` 配对与 `liveCompactRetainBudget`。
- [ ] **Step 3:** 提交信息标 `Scope: G-MG`；随后 `make check-upstream` 必须绿（该工具只比较已提交文件，提交前红是预期形态）。
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

## 明确不在本计划内

- B2 账本 JSONL / 只读 API / 前端上下文 chip / 多节点推广。
- B3 抽取卡片。
- 任何 `mode=on` 的线上启用（B1 代码已就绪，灰度顺序见 spec §6）。
- （B1 折叠实现原不在本计划内，2026-11-18 由用户指令「把 B 全部做完」追加为 Task B1。）
