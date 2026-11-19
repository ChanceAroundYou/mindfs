# MindFS 原生上下文活压缩（Live Compact）设计

- 日期：2026-11-18
- 状态：设计草案（待批准）
- Scope：G-MN（暂定，实现时按清单顺序确认）
- 参考实现：`Tranquillitatis-Lab/dsh-live-compact`（MIT，仅参考策略，不引入代码或依赖）

## 1. 背景与意图

MindFS 的 Claude 会话每轮把完整历史重新发给上游。长会话的代价不是「存不下」，而是：

- 每轮重复发送旧工具输出，token 成本随轮次增长；
- Claude Code 的 auto-compact 反复触发（本机一条会话 176 次 compact summary），每次压缩丢上下文、并重新注入 skill / plan 附件；
- 压缩后的附件已由现有代理确定性剥离（`server/internal/agent/proxy/strip.go`），但**工具输出和旧回合正文没人管**。

`dsh-live-compact` 证明了一条更便宜的路：不调模型、不注入工具，只在发给上游的那份副本里把旧内容换成本地抽取的卡片/指针，并保持前缀缓存。对比另外两条路：

- `billion-context` 依赖模型写摘要 + 4 个注入工具，且通过改写 `~/.claude/settings.json` 生效——本机 `render.sh` 每次渲染都会覆盖，还会与 MindFS 自己的 per-session 代理形成双代理链；
- `acp-kernel` 是 20.5k LOC 的纯 TS 库，吸收成本高，且同样依赖模型摘要与工具协议。

**意图**：把 live compact 做成 MindFS 的原生能力——不是外部代理、不是插件，而是内置在 MindFS 已有的 per-session 代理里，按节点配置、可观测、可回滚。

## 2. 目标 / 非目标 / 反目标

### 目标

- **G1 无损优先**：第一阶段只做「长工具输出 → 指针 + 本地存档」，原文永远可从本机取回。
- **G2 零模型调用**：折叠决策与渲染完全本地、确定性。
- **G3 前缀稳定**：只折叠「刚离开保留窗口」的内容；已折叠内容逐字节不变；每次折叠只造成一次尾部缓存断裂。
- **G4 失败开放**：任何解析 / IO / 渲染失败 → 原请求透传，绝不阻断会话。
- **G5 可观测**：每会话可见折叠块数、估算节省、真实 usage（input / cache_read / cache_write）与估算误差。
- **G6 节点本地可控**：默认 `off`；`shadow` 只观测不生效；可一键关闭。
- **G7 不改变既有边界**：不碰本地 transcript、不新增鉴权、不改变上游解析、不影响 Codex / ACP 会话。

### 非目标

- 不替代 Claude Code 自己的 auto-compact；两者必须共存。
- 不做模型生成摘要（B1 / B2 都不做），不做语义检索工具。
- 不向模型注入新工具；回读原文用模型已有的 `Read` / `Bash`。
- 不覆盖 Codex / ACP 会话（协议不同），只覆盖 `ProtocolClaudeSDK` 会话。
- 不做跨节点共享的上下文库；存档是节点本地的。
- 本设计不包含「整回合抽取卡片」的结构性改写（见 §6 Phase B3，需另一次设计评审）。

### 反目标

- 让请求出现 400、工具对断裂、角色交替错误。
- 省下的 token 被缓存未命中抵消。
- 出问题后无法定位、无法关闭、无法解释。

## 3. 约束（来自仓库与运行环境）

- 代理启动失败必须保持非致命（现有行为）。
- 请求 body 上限 32MB，不得放大；不得把整段响应缓冲后转发（SSE 必须逐块下发）。
- 不修改 `~/.claude/settings.json`；所有注入继续走 `--settings`（`pool_open.go` 已实现）。
- 本机 system 单元重启仅用户执行；A 阶段验证需要用户配合。
- 新增文件与改动必须登记 `docs/upstream-customizations.yaml`（G-MN），并在提交信息标 `Scope: G-MN`。
- `make check-upstream` 只比较已提交文件；登记必须在提交前写全。

## 4. 方案对比

| 方案 | 形态 | 增量成本 | 前缀缓存 | 与 MindFS 的冲突 | 失败模式 |
|---|---|---|---|---|---|
| **A. 现有代理内折叠（选中）** | `proxy.go` 新增 fold 阶段 | 0 | 可控（只动尾部） | 无 | 原请求透传 |
| B. 独立 sidecar（bili 式） | 额外进程 + settings 改写 | 模型摘要 + 工具往返 | 设计目标 | `render.sh` 覆盖、双代理链 | 会话可能既压不了也读不回 |
| C. 吸收 acp-kernel | Node sidecar + 20.5k LOC | 模型摘要 | 部分 | 状态 schema、tool-call 流拦截 | 同上 |
| D. SDK / agent 层改写 | 在 `claude.Session` 内改消息 | — | — | SDK 不暴露请求体，agent 层只看到事件 | 不可行 |

选 A：唯一能同时满足「零模型调用、零新进程、零 settings 改动、失败开放」的接缝。C 的 Content-Cached Retrieval 思路被吸收为 A 的「指针 + 内容寻址存档」，但不引其代码。

## 5. 设计

### 5.1 接缝与生命周期

现有链路：

```
Pool.compactProxyEnv → 注入 ANTHROPIC_BASE_URL=/p/{token}
CLI → proxy.handleProxied → stripCompactPayload → 上游
```

新增 fold 阶段，顺序固定：

```
读 body（≤32MB）
  → stripCompactPayload（现有，marker 驱动）
  → foldRequestBody（新，结构驱动）
  → 转发上游
响应：TeeReader → usageScanner（只解析 SSE data 行）
```

- `proxy.Server` 增加一个 `LiveCompact` 配置快照；`RegisterSession` 增加 `sessionKey` 参数（用于存档路径前缀与会话账本键）。现有调用点只有 `pool_open.go` 一处。
- `sessionState` 增加计数器与最近一次 usage；`PostCompact` 信号只清计数并记 epoch（fold 本身无状态，见 §5.5）。
- 所有新逻辑包在 `recover` 与错误返回中：出错返回原 body，并记一行 `[live-compact] skip reason=...`。

### 5.2 线上请求的不变量（必须由测试钉死）

- **I1** 消息数量、顺序、`role` 不变（B1 只改块内文本）。
- **I2** `tool_use.id` 与 `tool_result.tool_use_id` 的配对关系不变；id 本身不变。
- **I3** `thinking` / `redacted_thinking` / `image` / `document` 块字节不变；含非文本块的 `tool_result` 整块跳过。
- **I4** 非折叠块解码后的值不变（顶层 JSON 键序允许被编码器规范化）。
- **I5** 同一输入两次折叠 byte-identical。
- **I6** 追加一轮新消息后，新请求中「第一个被修改块之前」的前缀与前一次请求 byte-identical。
- **I7** 任何异常 → 返回原 body，HTTP 行为不变。

### 5.3 选择算法（B1）

输入：解析后的 `messages` 与节点配置。

1. **配对回合**：assistant 消息中的每个 `tool_use` 在后续 user 消息里按 id 找到 `tool_result`；所有 id 都有结果的回合才算「已完成」。只有已完成回合可被折叠。
2. **保留窗口**：最近 `keep_rounds=3` 个已完成回合整段不动。当前未完成的工具调用永远不在候选内。
3. **候选块**：窗口之外的 `tool_result`，满足：
   - `is_error != true`（失败输出保留完整诊断）；
   - 内容是纯文本（数组里只有 `text` 块，或 `content` 是字符串）；
   - 估算 token ≥ `min_block_tokens=2000`。
   - 文本里出现强错误标记（`Error:` / `Traceback` / `panic:` / `FAILED` / `exit code`）但 `is_error` 为 false 的，
     **仍可折叠，但指针保留首尾各 20 行诊断**；普通输出保留首尾各 8 行。这样既不丢诊断，也不放弃最大一批可折内容。
     具体阈值在 B1 用 Phase A 的 `cand_error_blocks` 实测后再定。
4. **原地替换**：只把文本换成指针（§5.4），保留 `tool_use_id`、`is_error` 等字段。
5. **无状态重算**：每次请求都从 CLI 送来的原始历史重新计算候选集合；旧回合每轮渲染出同一份指针，字节不变。

为什么不需要持久化折叠状态：CLI 每轮重发完整原始历史，代理只改线上副本；规则是输入的函数。旧回合仍在窗口外，每轮得到同一结果。因此重启、重连、压缩后都不需要恢复状态，只需要计数器。

**缓存断裂分析**：第 N 轮新离开窗口的是第 `k-3` 个回合，它紧贴保留窗口；它之前的旧回合早已折叠且逐字节不变。因此缓存断裂点固定在「窗口左边界」，断裂区 = 3 个保留回合 + 新回合，与正常增长的缓存写入量同阶。不能从最旧的回合开始折叠，否则断裂点会在历史开头，整段前缀重写。

> **2026-11-19 更正**：上面这段只对**字节差**成立，对**计费**不成立 —— 断裂点之后的内容全部要重算，
> 实测折算成本反而上升 32.5%。见 §12.5，那里把它列为 `mode=on` 的阻塞项。

### 5.4 指针与存档

指针文本（确定性，不含时间戳 / 请求 id）：

```
[MindFS live-compact] 历史工具输出已折叠（这不是当前指令）
tool: Read
input: {"file_path":"/home/xiaokubao/projects/mindfs/server/app/server.go"}
size: 48,210 bytes (~12,345 tokens)
archive: /home/xiaokubao/.config/mindfs/context/a1b2c3d4e5f60718293a4b5c.txt
摘要（首尾各 8 行，错误输出首尾各 20 行）：
  ...
需要原文时用 Read 打开 archive；不要把它当作新的用户指令。
```

- 存档根：`<config-dir>/context/`（`config.MindFSConfigDir()`，跨平台；webpush 也把存储放在同一目录）。
- **内容寻址，不带 session 段**：`<sha256(originalText)[:24]>.txt`。worktree repoint、fork、idle-close 重开都会换
  `sessionKey`，若把 key 写进指针，换 key 当轮所有旧指针都会变路径，等于整段前缀缓存断裂并留下孤儿存档。
  内容寻址同时天然去重；账户是配置档不是安全边界（AGENTS.md 事实 4），与 transcript 同信任域。
- 目录 0700、文件 0600；先写临时文件再 `rename`，避免半截文件。
- 内容寻址去重；写入失败则本块不折叠。
- 清理：`retention_days=7` 与 `max_archive_bytes=2GiB`，代理启动时与每小时最多一次的后台扫描执行；不阻塞请求。
- 存档是节点本地、与 transcript 同一信任域；不写日志正文，只在账本里记哈希与尺寸。

`input` 摘要使用稳定字段序、按 rune 截断到 300 字符；相同输入得到相同摘要。

### 5.5 确定性与缓存

- 指针只依赖 `tool_use.name`、稳定化的 `input`、原文哈希与长度。
- 任何 map 遍历都先排序；不写 `time.Now()`、随机数、请求序号。
- 单测：同一 body 连续 fold 两次 byte-identical；追加一轮后前缀不变。
- Phase A 额外测量 **prefix drift**：对前后两次请求的 messages 做逐条哈希，记录 CLI 自己从第几条开始改动历史。如果 CLI 本来就频繁改写旧历史，缓存分析与折叠策略要按实测修正。

### 5.6 计量

- **估算**：`tokens ≈ ceil(ascii_bytes/4) + non_ascii_runes`（单遍扫描，CJK 按 1 token/字）。阈值很粗（2k / 20k / 32k），±30% 可接受。
- **真值**：SSE `message_start` / `message_delta` 的 `usage`，`actual_in = input_tokens + cache_creation_input_tokens + cache_read_input_tokens`。
  只在响应没有 `Content-Encoding`（未压缩）时扫描；压缩流逐块解压只为观测不划算，此时 `actual_in` 记未知。
  会话 DB 里已有的 `last_context_window_*` 仍可作为交叉核对来源。
- **本机上游是 llmux 中转，usage 不可全信**（memory `llmux-usage-inflation`：同一会话 6 秒内 124k→359k）。
  因此 B1 的省量判据以**代理自己测的 `est_in` / body 字节数 / 折叠前后差值**为准；`actual_in` 与
  `cache_read/cache_write` 只做同节点前后对比与趋势参考，不当作绝对值证据。若中转根本不支持
  Anthropic 缓存，折叠的经济性反而更强（每次都是全价 token），设计不需要为此改动。
- **Phase A 日志**（每个大请求一行，只在 `shadow` / `on` 模式）：

```
[live-compact] shadow token=9f2a1c3d msgs=412 est_in=182340 actual_in=176912
  cand_blocks=14 cand_tokens=61200 keep_rounds=3 drift_at=390 fold_ms=38
```

- **会话计数**：requests、folded_blocks、folded_tokens_est、cache_read、cache_write、last_actual_in、post_compact_epoch。
- **Phase B2** 把每个会话的计数与折叠事件写入 `<data-dir>/context/ledger/<session-hash>.jsonl`，供只读 API 与前端使用。

### 5.7 配置与开关

节点本地 `config.json`（沿 Router 的既有做法：startupConfig → flag → `app.StartOptions` → Pool；flag 优先于文件）：

```json
"live_compact": {
  "mode": "off",
  "keep_rounds": 3,
  "min_block_tokens": 2000,
  "archive": true,
  "retention_days": 7,
  "max_archive_bytes": 2147483648
}
```

- `mode`：`off`（零开销，不解析 body）/ `shadow`（只测量与记日志）/ `on`（生效）。
- 默认 `off`；本机验证通过后按节点逐个改 `on`。
- **运行中急停**：存在 `<config-dir>/context/DISABLED` 时，代理按 `off` 处理（每秒最多 stat 一次，不重启）。
  本机 system 单元只有用户能重启，不能把「关掉」寄托在重启上；这个文件是 Agent 可写、立即生效的刹车。
- **请求范围**：只处理 `POST .../v1/messages`；`count_tokens` 与其他路径一律不碰——折叠 `count_tokens`
  会让 CLI 按缩小后的上下文计时，属于未批准的行为改变。
- **会话范围**：`configDir` 非空的隔离会话（Router / workflow 推荐器 / 结果转换器）只观测不折叠：
  它们继承不到用户的 `bypassPermissions`，指针里的 archive 可能读不了，失败会退化成「有损」；
  这些会话也不应影响 B2 面向用户的节省账本。
- flag：`-live-compact-mode`、`-live-compact-keep-rounds`、`-live-compact-min-block-tokens`；flag 优先于文件。
  `min_body_bytes` 与 `max_drift_messages` 只走文件，属于调参项，不占长期 CLI 参数。
- 关闭 = 改回 `off` 并重启；不需要清理任何持久状态（存档可留待保留期清理）。

### 5.8 失败模式

| 情况 | 行为 |
|---|---|
| body 不是合法 JSON / 没有 `messages` | 透传原 body，日志 `skip=parse` |
| 消息结构无法配对 / 未知块类型 | 跳过该段，不影响其它块 |
| 存档写入失败 / 磁盘满 | 该块不折叠，日志 `skip=archive` |
| 折叠后自检失败（不变量 I1–I4） | 丢弃改写结果，透传原 body |
| 上游返回 4xx | 无法归因时由 kill switch 关闭；Phase A 先收集 4xx 与 fold 的关联日志 |
| 代理 panic | 现有进程级恢复 + 本次请求透传 |

### 5.9 与 Claude Code auto-compact 共存

- fold 降低上游实际 input，CLI 从 `usage` 读到的上下文随之下降，auto-compact 自然推迟——这正是目标之一。
- 本设计**不禁用** auto-compact。PostCompact 时历史被 CLI 重写，fold 在下一请求按新历史重算；计数与 epoch 重置。
- 如果实测发现 fold 与 auto-compact 相互抵消，再单独评估「会话级 `DISABLE_AUTO_COMPACT`」，不在本设计内。

## 6. 分阶段与验收

### Phase A：只观测（先在本机）

- 配置管线、估算器、`usageScanner`、prefix drift、shadow 日志、单测。
- **不改写任何请求**：shadow 下断言 body 与输入 byte-identical。
- 验收：本机真实会话跑满 24h；日志可解析；给出估算误差分布与候选折叠量；零行为变化。

### Phase B1：无损折叠

**状态（2026-11-18）：已实现并提交，未部署、未在任何节点开 `on`。** 实施与实测见 §12.3；开闸证据
（真实流量下的候选出现率、节省比例、0 parse 失败、drift 频率）仍需 Phase A 在节点上跑满再取。
**2026-11-19 追加阻塞项**：隔离实测显示折叠让前缀缓存整体失效、折算成本反而 +32.5%（§12.5），
这一项不解决就不进灰度。

- 工具输出 → 指针 + 存档；不变量、失败开放、确定性、GC；单测 + 代理集成测试。
- **开闸证据**（必须来自 Phase A 真实数据，不能凭预测）：候选块在长会话里持续出现；单请求预计节省达到
  可观察量级（阈值在 A 结束时按分布定，不低于 total 的 20%）；parse 失败率为 0；prefix drift 不频繁。
- **灰度顺序**：先 WSL 或 fn（Agent 可自行重启 user 单元）开 `on` 观察 24h → 本机切 `on`（需用户重启
  system 单元）观察 24–48h。pc-win 最后。
- 验收：无归因于 fold 的上游 4xx；工具对/角色不变量全部成立；代理自测的 `est_in` 与 body 字节数下降；
  `cache_read/cache_write` 只作同节点前后趋势参考（llmux 数值不可信）；auto-compact 次数不上升；无新增压缩异常。

### Phase B2：产品面

- 每会话账本 JSONL + 只读 API + 前端最小状态（会话页一个「上下文」chip，点开看折叠摘要与节省）。
- 按节点推广到 wsl / fn / pc-win；`deploy-all.sh` 全绿。
- 登记 G-MN（`docs/upstream-customizations.yaml`：文件、改了什么、为什么保留、可见症状、针对性测试表）、更新 `docs/plans/README.md`。

### Phase B3：抽取卡片（需另一次设计评审）

- 把旧回合的正文（user / assistant 文本、命令与错误行）抽成卡片，保留路径、标识符、错误行、义务句。
- 会改变消息结构（增删/合并消息），必须单独论证角色交替与 tool 配对，不在本设计的批准范围内。
- 其他优化（增量计划缓存、跨节点统计汇总、自适应阈值）同属 B3。

## 7. 测试策略

- **单元**：估算器、回合配对、候选选择、指针渲染、存档路径、失败开放、确定性（两次 byte-identical）。
- **Golden**：合成 Anthropic wire body，覆盖字符串 content、数组 content、多 tool_use、is_error、image、thinking、未知块。
- **代理集成**：假上游记录收到的 body；断言改写生效且 SSE 仍逐块下发；断言 `off` / `shadow` 零改写。
- **并发**：`go test -race ./server/internal/agent/...`。
- **变异验证**：故意破坏「不折 tool_use」「错误结果不折」「前缀不变」三条，确认测试会红。

## 8. 风险与缓解

| 风险 | 缓解 |
|---|---|
| 折叠导致缓存未命中，省钱变亏钱 | 只折窗口左边界；前缀不变量测试；Phase A 记录真实 cache_read/write。**已实测命中（2026-11-19）**：折算成本 +32.5%，见 §12.5 —— `mode=on` 开闸前必须先解决 |
| Claude Code 改 wire 形状 | 结构驱动而非文本 marker；未知结构跳过；golden 随 CLI 版本更新 |
| 模型把指针当指令 / 忘记回读 | 指针显式写「不是当前指令」并给绝对路径；Phase B1 观察真实会话的回读行为 |
| 存档含敏感输出 | 同 transcript 信任域；0700/0600；不记正文；保留期与容量上限 |
| 磁盘增长 | 内容寻址去重 + 双上限 + 每小时最多一次 GC |
| 四节点配置漂移 | 节点本地文件是唯一事实；启动日志打印生效配置；部署脚本不覆盖 `config.json` |
| 长 body 解析拖慢首字节 | 只在大 body（默认 >100KB）才解析；Phase A 实测 `fold_ms`，超预算再做增量缓存 |

## 9. 重点（执行时的优先级）

1. **安全不变量 > 压缩率**：任何一条不变量不成立就不上线。
2. **缓存稳定 > 原始 token 节省**：宁可少折，不可每轮换前缀。
3. **先测量再开闸**：Phase A 没有真实数据前，不定阈值、不切 `on`。
4. **先无损再有损**：B1 只做可回读的指针；抽取卡片要另一次评审。
5. **可回滚**：`off` 必须零开销、零残留、立即生效。

## 10. 留给构建者的决定

- `StartOptions` / `agent.NewPool` 的具体签名与兼容包装（测试调用点较多）。
- SSE usage 扫描的缓冲上限与异常流处理细节。
- 存档 GC 的触发节奏与日志格式。
- 账本 JSONL 的字段命名与前端 chip 的具体位置。
- Phase A 日志的采样策略（是否每个请求都记，还是每会话每回合一条）。

## 11. Builder check 裁定（2026-11-18）

独立审阅提出的问题与结论，均按「更安全的一侧」落定：

| 问题 | 裁定 |
|---|---|
| llmux 中转的 usage 不可信，缓存经济无法直接验证 | 省量以代理自测的 `est_in` / 字节数 / 折叠前后差值为准；`actual_in` 与缓存计数只做趋势 |
| shadow 永远不折，B1 用什么证据开闸 | 用 A 的候选分布 + 预计节省 + 0 解析失败作为证据；灰度先 WSL/fn，后本机 |
| 本机急停依赖用户重启 | 增加 `<config-dir>/context/DISABLED` 文件刹车，每秒最多 stat 一次，立即生效 |
| 代理会看到 `count_tokens` 与辅助请求 | 只处理 `POST .../v1/messages`；`count_tokens` 不碰；辅助会话各自独立上下文 |
| 隔离 ConfigDir 会话读不到 archive | 这些会话只观测不折叠，也不进面向用户的节省账本 |
| sessionKey 在 repoint/fork 后变化 | 存档改为纯内容寻址，不带 session 段；指针路径跨 repoint 保持稳定 |
| `is_error=false` 但含错误文本 | 可折叠，但指针保留首尾各 20 行诊断；普通输出首尾各 8 行 |
| `<data-dir>` 指向安装目录会随二进制移动 | 统一用 `<config-dir>/context`，隔离实例（`XDG_CONFIG_HOME`）自然隔离 |
| 观测未按路径门控 | A 的实现只观测 `POST .../v1/messages`，并在日志里带上路径类别 |

## 12. Phase A 实施记录（2026-11-18）

按 §10 留给构建者的决定实际落定如下（实现位于 `server/internal/agent/proxy`，计划见
[plans/2026-11-18-mindfs-native-live-compact.md](../plans/2026-11-18-mindfs-native-live-compact.md)）：

- **签名与兼容包装**：`proxy.NewServer()` 保留并等价于 `NewServerWithLiveCompact(LiveCompactConfig{})`；
  池侧新增 `agent.NewPoolWithOptions(cfg, PoolOptions)`，`agent.NewPool` 等价于空选项。
  `StartOptions` / `PoolOptions` 只多出 `LiveCompactDisableFile`，其余沿用字符串/整数。
- **采样策略**：每个满足条件的请求记一条（不做会话内采样）；`parse_ms` 与 `est_in` 一并落地，
  便于直接算 p95 与解析成本。日志字段最终为
  `mode token scope msgs rounds closed keep epoch est_in actual_in cand_blocks cand_error_blocks cand_tokens cand_bytes drift drift_age parse_ms`；
  `actual_in=-1` 表示响应被压缩或没有 usage，`drift_age` 是首个漂移消息距上轮尾部的距离（-1 = 无漂移）。
  该行由 `liveCompactTurn` 保证「同一次请求的估算配同一次请求的 usage」，与两半到达先后无关（§12.2）。
- **usage 扫描**：只认 SSE `data:` 行，接受 `message.usage` 与顶层 `usage`；行缓冲上限 1 MiB，
  超长残行直接丢弃（下次 `\n` 重新同步）；`Content-Encoding` 非空时跳过扫描。
- **错误文本判定**：`Traceback (most recent call last)` / `panic:` / `fatal:` / `exception` /
  `exit status` / `permission denied` / `no such file or directory` / `command not found` /
  `error:` / `failed`，只看正文**首尾各 20 行**且必须**行首命中**（`strings.HasPrefix`）；
  只影响 B1 指针摘录宽度，不影响是否折叠。
- **急停文件**：`app.Start` 在未显式配置时拼 `<config-dir>/context/DISABLED`；代理内 1 秒 stat 缓存。
- **诊断接口**：新增 `(*proxy.Server).SessionFoldAllowed(token)`，供 pool 测试与后续 B2 诊断使用。
- **测试证据**：`go test -count=1 ./...` 全绿；`go test -race -count=1 ./server/internal/agent/... ./server/internal/kanban/`
  全绿；`go build ./...`、`gofmt`、`go vet` 干净。覆盖 estimate、usage、wire 解析/配对/候选/drift、
  shadow 字节透传、off/shadow 坏 body、小 body 跳过、`count_tokens` 越界、并发同 token、压缩流、急停开/关/恢复、
  observe-only 会话、配置优先级，以及 turn 配对（4 例，做过变异验证）与队列字节预算（2 例）。
- **未做**：真实 24h 观测、B1 折叠加写、部署与任何节点重启。`mode=on` 启动时明确告警
  「observing only」，不会静默改变请求。

### 12.1 真实会话冒烟发现（2026-11-18，`ok` 模型，隔离代理）

用 `go run ./server/internal/agent/proxy/lcsmoke -model ok` 启动进程内 shadow 代理，再让真实 Claude CLI
（`claude -p`，`--settings` 只覆盖 `ANTHROPIC_BASE_URL`）连续 Read 6 个大文件。生产服务与生产配置都未参与。

- **候选量**：第 9 轮 `cand_blocks=6`、`cand_tokens=21,446` / `est_in=63,238`（34%）、`cand_bytes=84,187`。
  长会话里折叠量足以跨过 B1「≥20%」门槛。
- **估算误差**：首轮 35,707 / 35,335（+1.0%），末轮 63,238 / 67,928（−6.9%）。JSON 结构开销让估算偏低，
  作为粗阈值可接受；`actual_in` 仍只作趋势。**注**：这一节的 `est/actual` 配对当时还是错的（§12.2 第 2 条），
  数字本身是同一会话的近似量级，复核后的正确配对见 §12.2 第 4 条。
- **解析成本**：`parse_ms` 6–17 ms/请求（body 35–250 KB）。
- **prefix drift 假阳性（已修，两轮）**：CLI 每轮把 `cache_control` 断点搬到最新消息，按 raw bytes 哈希时每轮都报
  drift 且恒等于上轮最后一条。第一轮只剥 `cache_control`，**不够**——抓真实请求体后发现 `system` 的 `content`
  在轮次间还会从 block 数组翻成裸字符串。现改为解析后归一化哈希（§12.2 第 1 条）；修复后 `drift` 恒为 -1。
- **错误文本假阳性（已修）**：原「任意位置包含 error/failed」把普通 Go 源码判成错误输出（3 个候选里 2 个）。
  改为只看首尾各 20 行的行首标记后，同一会话 `cand_error_blocks=0`。
- **结论**：Phase A 的观测口径可用；B1 开闸仍需生产节点 24h 数据（A6 Step 2 起）。

### 12.2 复验：三个必须记住的缺陷与数字（2026-11-18 晚）

用户追问「你确定这个是修复好了么？有没有需要进一步优化的地方？」后做的独立复核。**第一轮 drift 修复
只对了一半**，另外两个缺陷是复核才暴露的。

**（1）drift 假阳性原来只修了一半。** 剥 `cache_control` 之后仍每轮报 drift，于是用临时反向代理抓了 7 份
真实请求体逐条比对。真实差异有两类，不止一类：

- `cache_control` 断点搬家（已修）；
- **`system` 消息的 `content` 在 001 与 002 之间从 block 数组翻成裸字符串**（`[{"type":"text",...}]` →
  `"..."`），`tool_result.content` 也在数组/字符串之间翻。

所以改为**解析后归一化哈希**（`normalizeMessageHash`：role + 按类型规范化 block，text 形状差异折叠、
`cache_control` 丢弃、tool 块保留 id）。判别力用测试钉住：真实文本改动仍必须改哈希
（`TestMessageHashesIgnoreTextShapeFlip` / `...ToolResultShapeFlip` / `...CacheControlRelocation`）。
抓包回放与真实代理路径现在都是 `drift=-1`。

**（2）`est_in` 与 `actual_in` 曾经来自不同的请求。** 分析挪到 worker 之后，分析 5–12 ms 就跑完，而响应流
要持续数秒、usage 在流结束才写。旧实现让分析读会话级 `lastUsage`，因此第 N 轮的估算几乎总是配上第 N−1 轮
的 usage——而 Phase A 的全部价值就是这一对数。现改为每请求一个 `liveCompactTurn`，两半各存一半、后到者
发射；压缩流/错误响应/上游失败不产生 usage 时，用 `actual_in=-1` 正常收口，既不悬挂也不留空洞。
`TestLiveCompactTurnPairsOneRequestEstimateWithItsOwnUsage` 等 4 个用例钉住，并做过变异验证（放开
「有分析就发射」后两个用例立刻红）。

**（3）解析成本 450 ms/8 MB，且队列内存无全局上限。** 用 8 MB 合成 body 实测：`encoding/json` 在本项目
的嵌套解码路径下是 **436 ms、58 MB 分配**（CPU profile：`stateInString` / `checkValid` / `decodeState.skip`
各占 15–33%，同一 body 被完整校验四遍）。结论与后果：

- 请求路径现在只做门禁 + 入队，实测 **3.5 ms/8 MB（3 allocs）**；原先那次 8 MB 防御性拷贝已删除
  （body 由 handler 持有且此后不再修改）。
- 队列新增**全局字节预算** `liveCompactRetainBudget = 96 MiB`（仅每会话 4 条深度不够：
  一个 body 就是几十 MB，几个活跃会话就能钉住数百 MB），job 执行时归还字节。
- **这条对 B1 是硬约束**：手写的单遍字节扫描器（不需要 `encoding/json`）在同一 body 上是 **73 ms**，
  也只快 6 倍。B1 必须内联改写，73 ms/4 MB 的真实请求仍不可接受，所以 B1 不能靠「换个解码器」过关，
  要么用增量/流式改写（只定位要替换的 `content` 区间、其余字节原样透传），要么按 §5.5 的确定性前提做
  结果缓存（同一 body 前缀命中即复用上一次的折叠计划）。这是 B1 设计评审必须先解决的问题。

**（4）估算误差随内容类型漂移。** 同一会话 7 轮：首轮 `est/actual` = 35,872/35,500（+1.0%），末轮
60,737/65,624（**−7.5%**）。偏差随代码与标点密度的上升而扩大（4 字节/token 的 ASCII 假设偏乐观）。
20k/32k 的粗阈值仍够用，但 B1 若用估算值决定「何时折」，阈值应留 10% 余量。


### 12.3 Phase B1 实施记录（2026-11-18 深夜）

**§12.2 第 3 条留给 B1 的问题已用测量回答**，结论是两条路都走：请求路径内联**字节区间扫描**（只定位、
不解码），叠加**按内容寻址的渲染缓存**（消掉滑出窗口之外所有块的重渲染）。

| 组件 | 文件 | 作用 |
|---|---|---|
| 扫描器 | `live_compact_scan.go` | 只记录字符区间，字符串靠 `bytes.IndexByte` + 反斜杠奇偶跳过，不解码；`scanBlock` 记 `textParts` 与 `nonText` |
| 选择与拼接 | `live_compact_fold.go` | `pairScanRounds` / `planFold` / `applyFold` / `verifyFold` |
| 指针渲染 | `live_compact_pointer.go` | 输入按固定偏好序归一化（与 JSON 键序无关）、首尾摘录、`encodeJSONStringLiteral` |
| 渲染缓存 | `live_compact_pointer_cache.go` | 以 `toolName‖inputSummary‖原始转义区间` 的 128 位 maphash 为键 |
| 存档 | `live_compact_archive.go` | `<config-dir>/context/<sha256(text)[:24]>.txt`，0700/0600，temp+rename，去重，7 天 + 2 GiB |

**实测（2,622,250 B body，40 轮 × 60 KB，37 个合格块；本机 Celeron N5105，单线程）**

| 步骤 | 耗时 | 吞吐 |
|---|---|---|
| `scanWireRequest` | 1.09 ms | 2400 MB/s，26 KB / 412 allocs |
| `planFold` 冷（37 块全解码 + 渲染 + 写档） | ~90 ms | 29 MB/s |
| `planFold` 热（37 块全部缓存命中） | **1.3 ms** | 2000 MB/s |
| `verifyFold`（只重扫改写后的 body） | 0.21 ms | — |
| 无缓存时解码全部候选（对照） | 25 ms | 105 MB/s |
| 改写后 body | 228,535 B | 原体的 8.7% |

- **每个请求只多一条新滑出窗口的回合**，所以稳态边际成本 ≈ 冷单块（约 2.4 ms）+ 热路径 2.5 ms，与
  会话长度基本无关；而旧实现（每轮全量解码）是 26–90 ms 且随长度线性增长。
- **热路径的键必须是原始转义区间**：解码后再哈希等于把要省的开销又做一遍。
- **不能用 sha256 做键**：本机没有 SHA 扩展，`crypto/sha256` 实测 97 MB/s —— 2.5 MB 候选要 22 ms，
  与它省掉的工作同量级。`hash/maphash` 实测 6.8 GB/s（同一 payload 0.35 ms），且种子每进程随机，
  输入无法定向制造碰撞；取两条独立 lane 组成 128 位键，因为碰撞的后果是把另一块的指针安到这一块上，
  是静默错答而不是报错。
- **缓存命中后仍要 `os.Stat` 存档**（存在且大小相符）：GC 是 7 天/2 GiB 的异步清理，指针指向一个已经
  被删或被截断的文件比不折叠更糟。两个方向都有测试（`TestFoldCacheRewritesVanishedArchive` /
  `TestFoldCacheRewritesTruncatedArchive`），去掉检查都会被钉红。
- **`verifyFold` 复用请求路径上已有的扫描结果**，只重扫改写后的 body：改写前校验从 11.7 ms 降到 0.21 ms。

**不变量与测试映射**

| 不变量 | 钉住的测试 |
|---|---|
| I1 消息数/顺序/角色 | `TestFoldPreservesMessageCountOrderAndRole` |
| I2 tool 配对与 id | `TestFoldPreservesToolPairing`、`verifyFold` |
| I3 thinking/image/多块结果不动 | `TestFoldSkipsToolResultsWithNonTextBlocks` |
| I4 未折叠块解码后不变 | `TestFoldLeavesOtherBlocksDecodeIdentical` |
| I5 同输入同字节 | `TestFoldIsDeterministic`、`TestFoldCacheHitIsByteIdentical` |
| I6 已折叠前缀不重写 | `TestFoldKeepsOldPointersStableAcrossTurns`、`TestFoldPointerIgnoresToolInputKeyOrder` |
| I7 异常回退原 body | `TestFoldFailsOpenOnMalformedBody`、`TestProxyFoldForwardsMalformedBody` |
| 两套解析器不漂移 | `TestFoldScannerAgreesWithReferenceAnalysis` |
| 门禁与急停 | `TestProxyFoldOffAndShadowForwardUnchanged`、`TestProxyFoldSkipsObserveOnlySessions`、`TestProxyFoldOnWithoutArchiveObservesOnly`、`TestProxyFoldRespectsDisableFile` |

**本轮修掉的真缺陷**：`foldRequest` 原先没有读急停文件 —— 急停只停了观测，没停改写。本机 system 单元
只能由用户重启，`DISABLED` 是唯一的不重启收手手段，因此这是必须堵的口（由
`TestProxyFoldRespectsDisableFile` 暴露）。

**仍未做**：B2（账本/API/前端）、B3（抽取卡片，需另一次设计评审）。B1 也还没有在真实流量上开过
`mode=on`；灰度顺序按 §6 保持不变。

### 12.4 Phase B2 实施记录（2026-11-19）

账本、只读 API、前端胶囊三段。B2 只读：不改写入路径，不改变 B1 折叠决策，`off` 模式下一个字节都不多写。

| 组件 | 文件 | 作用 |
|---|---|---|
| 账本 | `server/internal/agent/proxy/live_compact_ledger.go` | 每会话一个 `<config-dir>/context/ledger/<sha256(sessionKey)[:8]>.jsonl`，一次折叠一行；到 4 MiB 用自身尾部（2000 行）替换；只记大小与计数 |
| 汇总 | 同上 `summarizeLedger` / `SessionContextSaving` | `latest` 是最近一次折叠的快照（不是累计），`requests` 是保留历史里的次数，`max_saved_bytes` 是单次最大节省 |
| 池转发 | `server/internal/agent/pool_context_saving.go` | 代理未装配时返回零值而不是 panic（每个节点都有池，不是每个进程都有代理） |
| 用例 | `server/internal/api/usecase/session_context_saving.go` | 查会话 → `InferAgentFromSession` → `agentPoolSessionKey`，把池级 key 交给代理 |
| 路由 | `server/internal/api/http_routes.go` | `GET /api/sessions/{key}/context-saving` |
| 前端服务 | `web/src/services/agent/contextSaving.ts` | `appURL(..., params, nodeId)`：数据面必须显式带节点 |
| 前端状态 | `web/src/components/agent/useContextSaving.ts` | 挂载/切换/`pending` true→false 各拉一次；请求代际守卫防串会话 |
| 前端视图 | `web/src/components/agent/ContextSavingIndicator.tsx` | 无节省时 `return null`；点开是 portal 浮层 |
| 接线 | `web/src/components/action/ComposerTopChips.tsx` | 仅 `hasBoundSession && mode !== "command"` 时渲染 |

**为什么要先解析 agent 再查账本**：代理按**池级** key 记账（`<agent>-<sessionKey>`），
不是裸会话 key。前端拿到的只有裸 key，若直接透传就会永远读到空账本 —— 症状是胶囊不出现，
不报错。由 `TestPoolSessionContextSavingForwardsPoolKey` 钉住（同一代理里 `claude-session-1`
有 1 条、`session-1` 必须 0 条）。

**本轮修掉的启动级缺陷**：`LiveCompactArchiveDir` 从未接进启动路径 —— `PoolOptions` 有这个字段，
但 `server/app/server.go` 没赋值，于是生产上 `mode=on` 只会打一行「observing only」，折不了任何东西，
账本永远为空，胶囊永远不出现。现在默认 `<config-dir>/context` 并沿 `PoolOptions → proxy.LiveCompactConfig`
下传；由 `TestNewPoolWithOptionsWiresArchiveDir` 用真实请求钉住（只有 archive 那一路会写账本）。

**边界**：账本不含消息文本、工具参数、路径与会话键（键只进哈希文件名）；`recent` 只回最近 200 条但
前端只用 `latest/requests/max_saved_bytes`。隔离 ConfigDir 会话永远到不了记账点（它们不折叠），
所以用户看到的节省不会被 Router/workflow 会话灌水。

**测试映射**

| 行为 | 钉住的测试 |
|---|---|
| 路由存在且空 key 明确报错 | `TestSessionContextSavingRouteRegistered`、`TestSessionContextSavingRequiresKey` |
| 池级 key 不串账本 | `TestPoolSessionContextSavingForwardsPoolKey` |
| 无代理/nil 池不 panic | `TestPoolSessionContextSavingWithoutProxy` |
| archive dir 真的接进代理 | `TestNewPoolWithOptionsWiresArchiveDir` |
| 前后端路径一致、节点与 root 都带上 | `web/tests/context-saving-chip.test.mjs` |
| 无节省不显示、命令模式不显示 | 同上（`visible` 与 `hasBoundSession && mode !== "command"` 断言） |
| 回合结束才刷新、切换会话先清空 | 同上（`wasPending && !pending`、`setSaving(null)`，并断言无 `setInterval`） |
| 中英文案成对、占位符对得上 | 同上 |

**仍未做**：B3（抽取卡片，需另一次设计评审）。B1/B2 都还没有在真实流量上开过 `mode=on`；
灰度顺序按 §6 保持不变 —— 先 WSL 或 fn 开 24h，再本机（需用户重启），pc-win 最后。

**`mode=on` 下的 `drift` 是自噪声（2026-11-19 冒烟记录）**：处理器先折叠再观测，所以 worker 哈希的是
**折叠后**的 body，`drift` 会把「本轮新滑出的那一轮被我们换成了指针」也算成 CLI 改写了历史 ——
真实会话 `on` 实测 `drift=3/5/7`，同一会话 `shadow` 恒为 `-1`。这不是折叠缺陷，也不影响任何决策：
`drift` 只进日志，而 §6 的开闸判据是在 `shadow` 上取的（那时 body 按定义未被碰过）。若以后要把
`drift` 当作 `on` 模式下的健康指标，必须改成在折叠**前**的 body 上算，而不是把折叠边界当漂移。

### 12.5 缓存代价实测：折叠让前缀缓存失效（2026-11-19，阻塞 `mode=on` 开闸）

§5.3 的假设是「断裂点固定在窗口左边界，断裂区 = keep 回合 + 新回合，与正常增长的缓存写入同阶」。
隔离实例三臂实测推翻了这条假设。

工作负载同 Step 6c（**一条会话**、6 次顺序 93,755 B Read）。三臂：`shadow`（7441，不折）、
`on`（7442，`keep_rounds=2`，共折 4 块）、`control`（7443，`mode=on` 但 `keep_rounds=20`，
6 轮内没有可折块 —— 用来把「`on` 代码路径」与「实际改写」分开）。usage 取自真实转录的 assistant
记录（同一请求有两条重复记录，去重后按请求序号）：

| 请求 | shadow in / read（命中） | control in / read（命中） | on in / read（命中） |
|---|---|---|---|
| 1 | 8,503 / 9,360（52.4%） | 17,868 / 0（0%） | 8,503 / 9,360（52.4%） |
| 2 | 37,412 / 17,863（32.3%） | 37,332 / 17,962（32.5%） | 37,332 / 17,985（32.5%） |
| 3 | 37,332 / 55,340（59.7%） | 37,332 / 55,419（59.8%） | 37,332 / 55,377（59.7%） |
| 4 | 37,479 / 92,672（71.2%） | 37,332 / 92,832（71.3%） | **75,555 / 17,863（19.1%）** |
| 5 | 37,388 / 130,151（77.7%） | 37,332 / 130,221（77.7%） | **76,262 / 17,858（19.0%）** |

第 4 个请求起 `on` 开始折叠（日志 `fold blocks=1/2/3/4`，账本 5 行）。三件事：

1. **`control` 与 `shadow` 逐轮重合**（第 2–5 请求差在 ±0.2% 内），所以 `on` 的差别不是 `on` 代码
   路径造成的，是**改写本身**造成的。
2. **`on` 的 `cache_read` 从第 4 个请求起钉死在 ~17.86k**，只剩系统提示 + 工具定义 + 首条用户消息
   这段静态头；`input_tokens` 从 37.3k 跳到 75.5k，之后每轮 76.3k / 77.0k / 77.7k 继续涨。
   即**折叠后的整段历史每轮都按全价重算**。
3. 按 1x input / 0.1x cache read 折算（第 2–5 请求合计；`cache_creation` 三臂都报 0，故未计写入
   溢价）：`shadow` ≈ 179.2k、`control` ≈ 179.0k、`on` ≈ 237.4k —— **原始 token 少 24.7%，折算成本
   反而高 32.5%**。真实 Anthropic 账户还要为反复写入这条前缀付 1.25x，差距只会更大。

**机制（推断，与观测一致）**：前缀缓存按「到某个 `cache_control` 断点为止的字节」命中。折叠把窗口
左边界那块正文换成指针，改动位置**早于** CLI 放置的断点，于是上一请求写下的缓存条目整条失配；而
折叠边界自己没有任何断点，折叠后的前缀从未被缓存下来，所以每轮从头全价重算。§5.3 的「断裂区 =
keep 回合 + 新回合」只对**字节差**成立，对**计费**不成立：断裂点之后的所有内容都要重算，不只是
窗口内那几个回合。

**结论**：直接违反 §9 优先级 2（缓存稳定 > 原始 token 节省）和 §8 第一行风险（「折叠导致缓存未命中，
省钱变亏钱」）。**`mode=on` 在缓存问题解决前不得开闸**，§6 的开闸证据补一条成本闸门。

**候选方向（都要另一次设计评审，未实现）**：

- 在折叠区最后一块（最后一个被折叠的 `tool_result`）上补一个 `cache_control`，让折叠后的前缀逐轮
  变成命中，把「每轮全断」降回「每轮只断新增那一块」。这是改 wire 形状，需要论证与 CLI 自己那几个
  断点的交互（`drift` 归一化里的 `stripCacheControl` 已经会忽略它，不影响现有观测）。
  **2026-11-19 已被 §12.7(a) 否决**：本上游按最长公共前缀自动缓存，`cache_control` 完全不参与，
  该方向只在 Anthropic 直连下有意义。
- 或者退一步：按会话而不是全局开折叠 —— 只有实测折算成本为负的会话才折。这要求计量从「字节/估算
  token」升级到「按缓存计价」，当前账本没有这个维度。

**测量边界**：按 §5.6，llmux 的 `usage` 只作趋势证据；本节是**臂间同条件对照**（同一上游、同一模型、
同一工作负载、相近时间窗），不依赖 llmux 的绝对值。`cache_creation` 三臂全 0 说明该字段在本上游
不可观测，因此这里**低估**了真实写入代价。n=1 会话；`shadow` 臂更靠后的第 7 轮撞 200k 窗口并压缩
失败，与本节无关（本节只用两侧都正常的第 2–5 请求）。

### 12.6 Phase B4 设计：低频大折 + 单调边界（2026-11-19，用户批准）

§12.5 定位了根因（折叠边界每轮右移 + 折叠区没有自己的 `cache_control`），并给出一个反直觉的推论：
**在 0.1x 缓存读的计价下，频繁小折在算术上不可能为正**。删掉 10% 历史只省 1% 的钱，而边界每动一次
就要按 1.05x 重算断点之后的一切。§12.5 的受控实验里 `on` 的成本是 shadow 的 **1.325 倍**，正是
「每轮一刀」的直接后果；反过来，s1 的 57 分钟真实会话整场只有 **2 次**实际折叠事件（块数 0→1→2），
缓存健康度 99.3%，`est_in` 与 shadow 几乎重合。

**结论（本节的判据）**：live compact 的价值主张从「省 token」改为「防止撞上上下文窗口」。验收标准：
**不触发时零字节变化；触发时只为止损，折算成本 ≤ shadow × 1.10**。§12.5 已证明：同一负载下
shadow 会撞 `Prompt is too long`，`on` 不撞——这才是它存在的理由。

#### 对照：从 `dsh-live-compact` 吸收什么

| 吸收 | 出处 | 我们的落点 |
|---|---|---|
| 云端普通轮次一刀不切，只在收尾时切 | `plan.js:900-901` `if (cloud && !draining) return null` | `foldRequest` 的 rail 门禁 |
| 会话级上下闸（低于门槛不维护，超过大闸交给官方压缩） | `index.js:1666` | `rail_tokens` / 上闸交给 CC auto-compact |
| 预测下一轮（`last + maxTurnGrowth`） | `index.js:1717` | `current + growth > rail_tokens` |
| 单刀最小/最大收益（`chunkMin` 1000 / `chunkMax` 32000） | `plan.js:885` | `min_fold_bytes` / `max_fold_bytes` |
| 单个超大 read 结果不受收尾门禁约束 | `plan.js:519-525` | `large_block_tokens` |
| 最新卡片是硬地板，切点只右移 | `plan.js:834-852` | 会话级「已提交集合」，见下 |

**不吸收**：整段事件→卡片（那是我们的 B3，会改消息结构，违反 I1/I2/I3）；本地/云端双引擎；
接管官方压缩器（CC 的 auto-compact 不可接管，只用 `PostCompact` 观测）；dshlc 的绝对阈值
（20k/650k 是它的窗口与工具形态）。

#### 必须自己补的两条（dshlc 没有的问题）

1. **单调边界要靠「已提交集合」实现，不能靠指针缓存。** CLI 每轮重发原始转录，折叠是每请求重算的
   变换；如果「本轮不折」，此前折过的块就会**退回原文**——既毁缓存，又把上下文弹回去。而
   `pointerCache` 在 4096 条时会整体清空（`live_compact_pointer_cache.go:98`），不能当承诺记录。
   因此每个会话维护独立的 `committed` 集合（`map[pointerCacheKey]struct{}`，永不淘汰）：
   **已提交的块每轮无条件折回**，rail 门禁只决定「是否提交新的块」。
2. **折叠边界没有断点**，所以每次提交新块都会让断点之后的一切重算。这一点由「低频」缓解，不靠改
   wire 形状。补断点列为条件性 Phase C，单独评审与验证。

#### 新增配置（只走 `config.json`，不给 CLI flag）

> **下表是 2026-11-19 首次实施时的取值，已被 §12.7(d) 修订**（`rail_tokens` 160000 /
> `target_tokens` 40000 / `min_fold_bytes` 4096）。保留原值是为了让 §12.7 的对照与 Step 8 的实测
> 可追溯；代码默认值以 §12.7(d) 为准。

| 键 | 原默认 | 含义 |
|---|---|---|
| `rail_tokens` | 130000 | 触发线；`current + growth` 超过它才提交新块 |
| `target_tokens` | 100000 | 收尾低水位；一次事件折到估算上下文 ≤ 它 |
| `min_fold_bytes` | 32768 | 单次事件最少新折字节；不足则本次不提交 |
| `max_fold_bytes` | 262144 | 单次**块（chunk）**最多新折字节；一次事件最多 8 块 |
| `large_block_tokens` | 32000 | 单块达到此估算 token 时，即使未过 rail 也提交 |

默认值按 200k 窗口取：CC 的自动压缩阈值约在 `窗口 − 33k`（≈167k），rail 130k 留出一轮余量，
target 100k 留出两次普通增长。`rail_tokens = 0` 表示门禁关闭（等价于 B1 的「每个合格块都折」）。

#### 算法（`foldRequest`）

1. `est = estimateByteTokens(body)`（折叠前）；`growth = max(0, est − lastEst)`，首请求为 0。
2. 收集合格块（关闭的回合、`keep_rounds` 之外、非 `is_error`、纯文本、单 text part），按最老→最新排序。
3. **第一遍：提交集。** 命中 `committed` 的块直接生成替换；其 token 计入 `stickyTokens`。
4. `current = est − stickyTokens`。`fire = (current + growth > rail_tokens)`，或存在单块
   `tokens ≥ large_block_tokens`。
5. **第二遍：新提交。** 仅当 `fire` 时继续；按最老→最新渲染并提交，直到
   `est − stickyTokens − newTokens ≤ target_tokens`，或已排空 `foldMaxChunksPerEvent`（8）个
   `max_fold_bytes` 块，或没有更多合格块。**`max_fold_bytes` 是块上限而不是事件上限**（§12.7(d)）：
   事件上限会让一次只折两块，那是实测比不折更贵的浅折。
6. 若 `fire` 且 `newBytes < min_fold_bytes` → **放弃本次新提交**（只保留提交集）；不再「回头凑」。
7. 新提交的块写入 `committed`，并照旧写存档、渲染指针、`verifyFold` 校验；任一环节失败则整次回退
   原 body（I7 不变）。

`large_block_tokens` 触发的单块提交计入 `newBytes`，但仍受 `min_fold_bytes` 约束（保持「小折不折」
的规则统一）；它不改变第 6 条。

#### 新增不变量

| 不变量 | 含义 |
|---|---|
| **I8 已提交永不回退** | 同一会话内，一旦某块被提交折叠，后续任何请求都不得把它展开；`committed` 是唯一真相 |
| **I9 未触发零变化** | `fire=false` 且无新提交时，转发 body 的折叠区与上一轮逐字节一致；缓存前缀因此稳定 |

#### 观测

`[live-compact] fold` 行增加 `new_blocks= / sticky_blocks= / gate=`（`below-rail` / `fold` / `no-backlog`）；
`[live-compact] shadow` 行增加 `gate=` 与 `would_fold_tokens=`，让 shadow 能先预演同一套政策。账本不变
（`saved_bytes` 仍是本请求相对原始 body 的总节省）。

#### 验收（三臂，6×93,755 B Read，隔离实例）

1. `on` 的折叠事件 ≤ 2 次（现状 4 次）；
2. `on` 不撞 `Prompt is too long`，shadow 撞；
3. 折算 token 成本 `on ≤ shadow × 1.10`（按 §12.5 同法，第 2–5 请求）；
4. 末轮 `est_in` ≤ shadow × 0.8。

先做 Phase A（策略层），再在 WSL/fn 跑 24h `shadow` 预演（准入：≥3 次 `gate=fold` 且平均
`est_after/est_before < 0.8`）。Phase C（折叠边界补 `cache_control`）**已由 §12.7(a) 否决**——本上游
不认 `cache_control`，只在切到 Anthropic 直连时重开。默认值按 §12.7(d) 修订后需重跑本节三臂。

#### 边界与残留

- `committed` 是进程内状态：服务重启会让会话重新从零开始提交（重启本身会杀掉 CLI 子进程，不会
  出现「同一会话跨重启」）。
- 存档 sweep（7 天 / 2 GiB）在会话生命周期内不会删到在用文件；若文件意外缺失，重写存档得到同一路径，
  字节不变。
- 本节不解决 §12.5 的 `cache_creation` 不可观测问题；成本对照仍是臂间相对值。

### 12.7 缓存语义实测与「折深不折频」（2026-11-19，Step 8 之后的定向 spike）

Step 8 的 2.26× 说明 §12.6 的默认值选错了。在继续调参之前，先用一组**离线、可复现、无跨臂污染**
的 spike 把三件事测清楚：这条链路到底怎么命中缓存；`cache_control` 在我们这条路上有没有用；折叠的
收益到底由什么决定。脚本在 `/tmp/lcspike/`（一次性，未入库），全部经 llmux 走真实上游。

#### (a) 命中规则：最长公共前缀，与 `cache_control` 无关

每个请求加随机 UUID 前缀隔离跨臂污染（没有它，后一个臂的第一个请求会命中前一个臂写下的缓存，
这是最初两轮测量得出错误结论的原因）。同一份 8 轮转录，在固定位置改一处再发：

| 改动位置 | `cache_read` |
|---|---|
| 不动（基线） | 133,632 |
| 只改**最后**一条消息 | 117,248 |
| 改**中间**一轮 | 65,024 |
| 改**第一条**消息 | 0 |

**命中长度 = 改动点之前的字节数**，与 CLI 自己放的那几个 `cache_control` 断点无关。对照实验：Q1
（全程不带任何 `cache_control`）与 Q2（只在 `system` 上带一个）返回**完全相同**的
`hit=70,144`、`bytes=124,861`。所以本链路是上游按最长公共前缀自动缓存，`cache_control` 被忽略。

**这直接否决了 B5 的前提**：「在折叠区最后一块补一个 `cache_control`，让折叠后的前缀逐轮命中」在
本上游不可能生效。该方向只在切到 Anthropic 直连（`cache_control` 真正参与计价）时才有意义。

#### (b) 折叠收益由「折多深」决定，不由「折几次」决定

固定 8 轮负载，只在第 4 轮折一次、之后每轮原样重放同一份折叠结果（即 dshlc 的「切一次、写回会话」
语义，我们用 `foldCommits` 模拟）：

| 臂 | 折掉的块 | 折后字节 | 折算成本 | 相对不折 |
|---|---|---|---|---|
| A 不折 | 0 | 322,996 | 324,675 | — |
| D 深折 | 7 / 8 | 43,381 | **287,879** | **−11.3%** |
| E 中折 | 4 / 8 | 163,216 | 361,878 | +11.5% |
| F 浅折 | 2 / 8 | 243,106 | 426,379 | +31.0% |

另一组（每轮 80 KB）把「折几次」拆出来：A 不折 631,387；B 折一次浅的 734,147（+16%）；
C **每轮重折** 1,606,270（**2.54×**）。

**代价定律**：一次折叠把断点之后的一切按 1x 重算；它省下的是被折掉部分此后每轮本应有的 0.1x 命中。
设折前 `S`、折后 `F`、每轮增长 `g`，回本所需轮数

> `回收轮数 ≈ 10 × F / (S − F)`

深折（`S−F` 大）一两轮就回本；浅折要几十轮，而事件间隔只有几轮，所以**净亏**。这解释了 Step 8：
`rail=130k / target=100k` 相对 200k 窗口只砍掉约 15%，回收期约 33 轮，而门禁每 3 轮左右就触发一次。

#### (c) 与 dsh-live-compact 的真正差异不是阈值，是「切完就写回」

读参考实现（`/tmp/dshlc`）：`minTokens=20000`、`keepRounds=3`、`keepRoundTokens=32000`、
`chunkMin=1000`、`chunkMax=32000`、`pointerTokens=32000`、`bigGate=650000`，云端只在收尾（draining）
时切，最多 8 遍。但这些数字都不是它赢的原因：它把切点通过
`session.append("user/message", checkpoint, {surfaceOp:{op:"replace", startSeq, endSeq}})` **写进会话本体**，
下一轮请求的前缀逐字节不变，缓存从断点继续命中。我们是**线上代理**，CLI 每轮重发原始转录，折叠是
每请求重算的变换 —— 只能靠「切一次就再也不动」来逼近同样的前缀稳定性（`foldCommits` 就是为此而设）。

因此 dshlc 的绝对阈值不可照搬（20k 是它的窗口与工具形态），可搬的是**形状**：触发晚、一刀深、
a 之后不再切。

#### (d) 对 B4 默认值的修订（待真实三臂复测）

| 键 | 原默认 | 修订 | 理由 |
|---|---|---|---|
| `rail_tokens` | 130,000 | 160,000（按窗口比例） | 深折发生在接近但未越过压缩阈值时；130k 上游 ≈ 每 3 轮一刀 |
| `target_tokens` | 100,000 | 40,000 | 目标不是固定低水位，而是**尽量深**（(b) 的分数 `F/(S−F)`）；实际约束通常落到尾部预算 |
| `min_fold_bytes` | 32,768 | 4,096 | dshlc `chunkMin` 是 1k token；门槛不再是「够不够一次断点」，而是「折完是不是真的更小」；深折一次就过 |
| `max_fold_bytes` | 262,144 | 保持，但语义改为**块上限** | 一次事件最多排空 8 个这样的块（dshlc 的 8 遍 drain）；否则单块上限会把事件压成浅折 |
| `keep_tail_tokens`（新增） | — | 32,000 | dshlc 的 `keepRoundTokens`：受保护尾部按 **token** 封顶，不只按轮数。三小轮是合理尾部，三只 90 KB 读是 75k 尾部，rail 到时只剩一轮可折 |

**修订后验收标准不变**（§12.6 四条），但第 1 条「折叠事件 ≤2 次」从「限制频率」变成「形状约束」：
深折在 8 轮负载里应当只有 1–2 次事件。

**复测（2026-11-19，真实三臂，`keep_tail_tokens=32000`）**：PASS。折算成本 `shadow` 353,809 /
`on` 350,096（0.99×），`on` 2 次事件各折 3 块，`max actual_in` 129,994（shadow 203,075 并压缩失败），
末轮 `est_in` 比 0.77。只改阈值、不加尾部预算时是 2.0×（每轮只折一块的浅折）——**`keep_tail_tokens`
才是这一轮的关键**，阈值只是前提。详见计划 Step 8 复测第二次。

#### (e) 低 rail（20k / 50k）的直接实测：50k 不省钱，100k 才是免费的下移

(b) 的「每轮重折 2.54×」一度被读成「低 rail 必亏」。`keep_tail_tokens` 落地后先用同一负载
（8×93,755 B 顺序 Read、隔离实例）跑了五臂（折算成本 = `input×1 + cache_read×0.1 + output×1`，
`cache_creation` 本上游恒 0），其中 `rail50` 当时打平默认值。**该结论没有复现**：那批臂里 agent 的
读法不一致（有臂用 `offset`/`limit` 分块读、有臂重复读同一文件），把提示词改成「每文件只调一次
Read、不许带 offset/limit、不许重读」后重跑七臂，七条臂全部是 8 次整文件读：

| 组 | rail / target | 折算成本（各次） | 均值 | 上下文峰值 | 折事件 |
|---|---|---|---|---|---|
| 默认 | 160k / 40k | 340,190 / 367,914 / 357,041 | 355,048 | 129.6 / 129.7 / 129.7k | 3 / 3 / 2 |
| 100k | 100k / 20k | 339,933 / 380,111 | 359,822 | 93.2 / 95.2k | 4 / 3 |
| 50k | 50k / 20k | 394,534 / 389,015 | 391,775 | 58.8 / 61.6k | 7 / 7（各 1 块） |

（上下文峰值取 transcript 里每条 assistant 消息的 `input + cache_read`，是权威口径；运行间离散度
约 ±8%，所以「默认 vs 100k」是打平，「50k」是两条都落在默认三次之外、约 +10%。）

结论：

1. **低 rail 不比默认值便宜**：50k 两次都比默认三次贵（+10%）；100k 与默认打平。早先 347,897 的
   rail50 是读法混杂的产物，不作为依据。
2. **机制**：rail50 在「第一轮滑出保护窗口」的那一拍就触发，此后每轮都改写请求体——折叠区从系统头
   之后立刻开始，**整个前缀（除静态头 ~9.4k）不再命中缓存**，每次请求按 1x 全价。默认值前 3 个请求
   不折，能吃到 54–90k 的 `cache_read`（0.1x）；rail100 折在两者之间（2 块一次，峰值 95k）。
   低 rail 省下的原始 token 抵不过丢掉的缓存折扣。
3. **target 在 rail 50 上根本不起作用**：t40（早先）、t20（本次两条）、t2 三条独立臂的折叠序列逐块
   相同——7 次事件、每次恰好 1 块、每块 24,837 est。**折深 = rail 触发时已积压的可折轮数**，rail50
   每拍只积压 1 块，target 再低也无块可折；代码里的 break 条件只是「提前结束 drain」，不能制造 backlog。
4. **20k 不可达，与 target 无关**：全部 7 块折完后（`gate=no-backlog`、`would_fold_tokens=173,859`
   已是全部）发送体仍停在 47.9–51.9k est / 59–63k actual。再往下只能折模型正在读的那一轮（B3），
   dshlc 也不做（`keepRoundTokens=32000`）。
5. **默认值定为 100k / 20k（2026-11-20 用户批准）**：成本与旧默认（160k / 40k）打平，上下文峰值
   130k → 95k；50k 的峰值 59k 要付 ~10%，不采纳。三种组都还有 ±8% 的运行间噪声、n=2–3、且只有
   这一种负载形状 —— 这是本决定的已知边界。

**下一步（需单独授权）**：用「小回合与大读交错」的负载复测 100k vs 旧默认（那正是 (b) 里 2.54× 的
形状，也是 100k 唯一没覆盖的风险）；`mode=on` 保持不开。

**测量边界**：本节全部是真实 CLI + 隔离实例的端到端臂（agent 自己的读法已用提示词钉死，仍属人为
负载），不替代 §12.6 的三臂验收；`cache_creation` 在这些臂里同样恒为 0。

### 12.8 真实会话回放：小回合与大读交错的负载（2026-11-20，回答 §12.7 的「下一步」）

§12.7 的下一步和用户要求（「你确实需要去测试大小交错的，那才是真正的 agentic workflow，能不能
尝试去回放某一个真实的 mindfs 会话」）是同一件事。做法不是重建转录（太有损：`prompt_snapshot`、
无效 thinking 签名、CLI 内部记录都无法还原），而是**抓真实线上请求体再回放**。

#### 方法

- **抓包**：在 llmux 前放一个 MITM（`/tmp/lcbench/mitm.py`，一次性），隔离实例 7464（`ok` 模型）
  跑一个真实任务——「按体积顺序逐个完整 Read `server/internal/api` 下 60 个最大的 `.go` 文件，
  每文件只读一次、不许 offset/limit」。会话里真实发生了 **2 次 CLI 自动压缩**。
- **序列**：93 个 `/v1/messages` 请求体（83 个生成 + 10 个 `count_tokens`），被压缩点分成 A/B/C
  三段，body 13 KB → 558 KB，真实上下文峰值 **166,835**。
- **回放**：用真实 proxy 包写一个 bench harness（`/tmp/lcbench/lcharness`，不入库），把 body 按序
  POST 进 `NewServerWithLiveCompact` 起的随机端口、再转发真实上游，usage 取真实 SSE。走的是
  生产同一份折叠代码。
- **隔离**：每臂在 `system` 末尾注入唯一 nonce，防止三臂共享前缀缓存（§12.7(a) 的教训）。
- **成本控制**：把 body 里的 `max_tokens: 32000` 字节级替换成 `1`。冒烟对照（同一 body）：`in=5
  read=12881` 两种设置逐字相同，单请求 66 s → 10–40 s，输出 token 可忽略。
- **臂**：`off`（对照）、`on 160k/40k`（旧默认）、`on 100k/20k`（新默认）。每臂 92 行（`op` 型号的
  首个请求跳过）。

**负载形状**：每轮 3–15 KB 工具结果（p50 11 KB，最大 15 KB），约 35 轮把上下文推到 166k。不是
90 KB 大读，而是「多个中等结果、频繁小步」——正是 §12.7(b) 里最容易被浅折拖垮的形状。

#### 结果

| 臂 | 折算成本 | input | cache_read | 上下文峰值 | 新折叠事件 | A / B / C 折算 |
|---|---|---|---|---|---|---|
| off | 1,069,996 | 391,601 | 6,783,133 | 168,208 | — | 432,358 / 468,531 / 169,107 |
| on 160k/40k | 1,069,971（1.00×） | 473,502 | 5,963,868 | 158,795 | 2 | 424,741 / 476,118 / 169,112 |
| on 100k/20k | 974,809（0.91×） | 521,739 | 4,529,889 | **100,213** | 4 | 373,731 / 447,603 / 153,475 |

C 段的 153,475 是上游计量异常，不是折叠行为：C 段三臂 body 相同，on100 在这一段有一条 usage 全 0、
另有 4 条把本应计入 `input` 的量记成了 `cache_read`。把 C 归一到与 off 相同后 on100 =
**990,441（0.926×）**；下文按 ~0.93× 说。作为对照，同 body 的 C 段 off vs on160 只差 5 token
（0.003%），说明上游对相同请求的计量本身是可重复的。

逐事件的重写代价（on100，真实 usage）：

| 事件 | 折前 ctx | 折后 ctx | 折叠请求自身 `input`（1× 重算） | 折叠 token |
|---|---|---|---|---|
| 1 | 98,540 | 40,589 | 31,216 | 49,955（10 块） |
| 2 | 96,431 | 45,018 | 35,645 | +45,290（累计 22 块） |
| 3 | 100,213 | 58,984 | 49,611 | 36,488（10 块，B 段开头） |
| 4 | 99,783 | 63,628 | 54,255 | +32,869（累计 20 块） |

#### 结论

1. **形状对**：4 次事件、每次折 10–22 块，折后只有紧接的一轮按 1× 重算，之后逐轮命中；上下文
   在 ~100k 与 40–64k 之间往返。没有出现「每轮浅折」，与 §12.7(b) 的「折深不折频」一致。
2. **成本**：on100 ≈ 0.93× off（原始读数 0.91×），on160 = 1.00×。两者都满足 §12.6 的
   ≤ `shadow × 1.10`；**100k 反而更省**，因为它在增长曲线上更早触发，事件之后有更多轮可以摊薄
   重写。160k 两次事件发生在对话第 32/65 个请求，只剩 3–4 轮可摊薄：一次折 91,758 token 每轮省
   9,176，重写 35k，刚好够本。
3. **峰值**：168,208 → 100,213（−40%），稳定压在 CLI ~167k 自动压缩阈值之下。`off` 臂的峰值
   正是真实会话里触发两次自动压缩的位置——这是「不撞压缩」的直接证据（真实收益还包括：线上不会
   发生那两次压缩本身，以及压缩带来的上下文损失；回放里 body 序列是固定的，所以这部分收益被低估）。
4. **为什么必须分开记 saved 与 rewrite**：事件 1 的 `saved_bytes=373,760` 对应 `rewrite≈31k`
   token，只看 saved 会得出「省了 37 万字节」的结论；真实净收益要用两者相抵（§12.9 第 12 条）。

**测量边界**：n=1 个真实会话、1 种任务形状（顺序读 60 个文件）、`max_tokens=1`、`ok` 模型、
llmux 上游（其 usage 有已知估算问题）；不代表「大量小 grep」「长 bash 输出」等其他形状，也不代表
Anthropic 直连。

#### 50k/20k 复测与冷启动对照（2026-11-20，用户要求「对于这个会话再试一下 50k/20k 的」）

同一负载、同一 92 个 body、三个全新冷启动臂（唯一 nonce），其中 100k 重跑一次作为冷启动对照
（§12.8 的 on100 臂首个请求带了 9,373 token 的残留命中）：

| 臂 | 折算成本 | input（1×） | cache_read（0.1×） | 峰值 | 新折叠事件 |
|---|---|---|---|---|---|
| off（§12.8） | 1,069,996 | 391,601 | 6,783,133 | 168,208 | — |
| on 100k/20k 冷复跑 | 945,691（0.88×） | 516,354 | 4,292,549 | 97,008 | 3 |
| on 50k/20k | **1,212,902（1.13×）** | 946,959 | 2,658,612 | **66,968** | 37 |
| on 100k/20k + 预热 | 975,309（含预热 991,030） | 539,009 | 4,362,178 | 104,270 | 3 |

1. **50k 用成本买峰值，而峰值已经不是瓶颈**：cache_read 减半（2.66M vs 4.29M），但 1× input 几乎
   翻倍（947k vs 516k）——50k rail 下每三五轮就折一块，折叠边界每轮都动，折后请求几乎每次都要
   按 1× 重建 ~38k（seq 79–82 连续四条 `in≈38k`、`cache_read` 钉死 9,372）。净成本 +13% vs off、
   +28% vs 100k；换来峰值 66,968（比 100k 再低 31%），但 100k 的 97,008 已经压在 CLI ~167k 自动
   压缩阈值之下，再低没有对应收益。
2. **臂间方差 ~3%**：同配置三次读数 945,691 / 974,809 / 975,309。差异来自上游 512-token 缓存块
   对齐（第 2 个请求的 `cache_read` 就相差 256）与 rail 判定所用 `actual_in` 的累积差，折叠点因此
   前后错开一个请求、事件数 3 vs 4。50k 的 +13% 远在方差之外。
3. **预热另见 §12.9 末条**：3 次事件共 15,721（+1.7%），后续请求零收益。


### 12.9 与 dsh-live-compact（dshlc）v1.1.12 的对照审计（2026-11-20，用户要求）

对照对象是 **`dsh-live-compact`（下称 dshlc）v1.1.12**——DSH 宿主上的 compaction engine 插件。
DSH 自身的官方 pruner 与大摘要**不是**对照对象，只在 dshlc 显式与它们协作的两行（1、13）出现。
读的是 dshlc 源码（`/tmp/dshlc` 的一次性 checkout），不是 README。下表的 dshlc 列每个数字都可在
源码里找到（关键位置：`settings.js`、`index.js:1603-1998`、`plan.js:491-1421`、`shadow.js`、
`raw.js`、`cooldown.js`、`gate.js`）。

| # | 维度 | dshlc v1.1.12 | MindFS（B4） | 判断 |
|---|---|---|---|---|
| 1 | 位置 | DSH 的 compaction engine 插件；通过宿主 API（`surfaceOp replace`）写回 DSH 会话 surface，下一轮前缀逐字节不变 | HTTP 代理；每轮从 CLI 原始转录重算，`foldCommits` 保证同一指针（I5/I8/I9） | 等价效果、不同路线。代理位置让我们在请求路径同步折叠（实测 1–30 ms）；dshlc 要在请求前 wait barrier（3 s 超时，超时就放弃本次） |
| 2 | 触发 | `minTokens=20000` 下限、`bigGate=650000` 上限；local 一刀；cloud drain 在「>keepRoundTokens 或有增长余量」或 ≥48k 时触发、最多 8 遍、降到 ≤32k | rail 100k、每事件 ≤8 块、target 20k（实测非 binding）、尾部 `keep_rounds=3` + `keep_tail_tokens=32k` | 形状同源（晚触发、深折），数值按我们的窗口与实测重定 |
| 3 | 块门槛 | `chunkMin=1000` token | `min_block_tokens=2000` + `min_fold_bytes=4096` | 同量级 |
| 4 | 长读豁免 | `index.js:644` 把「最新轮 `read` 且结果 ≥ `chunkMin`」标成 `longRead`；`roundHasLongRead` 据此单独处理该轮；单结果超 `pointerTokens`（默认 0 → 跟随 `chunkMax`=32k）只留指针 | `large_block_tokens=32k`、不限工具、整轮可折 | 我们保守 32 倍；本会话 15 KB 结果不触发。理由见 §12.7(b)：早切会把模型正在读的结果抽走，重复读=每轮重折 |
| 5 | 失败结果 | dshlc 结构性识别（`isError`、`[stderr]` 前缀、退出码形式、JSON `exitCode`）；span 内含失败时保留诊断，pointer-only 拒绝含失败 | `isError` 一律不折；未标记但首行像报错的文本只加宽摘录（首尾各 20 行） | **最实质的缺口**：一个 200 KB 的失败构建日志我们永远不折。本会话 58 条 error 结果全部 ≤329 B，无实际影响，但形状上应补 |
| 6 | 多文本/非文本块 | dshlc 文本投影合并全部 text 块进卡片；非文本不进卡片但 span 仍替换 | 恰好 1 个 text part 才折（保持线上块结构，I3/I4）；非文本跳过 | 我们保守；本会话 0 例 |
| 7 | 卡片 | dshlc 结构化抽取：保留行带角色、工具证据（调用+退出码）、后台任务、原文指针；明确「本地备份不在这次请求里」，路径只作为 metadata | 每块指针：tool/input/大小/archive 路径/首尾摘录 +「需要原文用 Read」 | 各有取舍：我们的原文可读回，dshlc 的证据在「模型读不到备份」时仍留在上下文。B3 可参考其证据分层 |
| 8 | 指针身份 | dshlc 用会话日志事件号；明说不给备份路径 | 内容寻址的绝对存档路径 | 取决于工具可达性；我们有 Read，是特性 |
| 9 | 备份失败 | dshlc 整段不切（每个结果都必须有已验证 raw） | 该块不折、其余照折 | 等价 |
| 10 | 保留 | dshlc 256 MiB；轮转时**保护被引用路径**（`shadow.js:1450-1574`）与其他活跃会话 | 7 天 / 2 GiB，按时间与总量删，**不保护被引用路径** | **缺口**：磁盘压力下可能删掉仍被 sticky 引用的存档 |
| 11 | 重启 | dshlc 恢复持久化游标；无游标回退最近 `keepRounds` 个结果事件；raw 另做校验恢复 | commits 只在内存，重启退化为空集（I9 已记录） | **缺口**：重启后下一轮整段原样发出，直到 rail 再触发；本会话形状下最多多付一个 ~100k 的 1× 请求 |
| 12 | 计量 | dshlc 把 `saved` 与 `rewriteCharged` 分开记账（`shadow.js:867`），`stats` 两个都报，并明说 saved×$0.15/M 未扣重写 | 账本只有 `saved_bytes`，胶囊只显示省了多少 | **缺口**：§12.8 正好说明为什么必须分开——一次折 91,758 token 的 saved 对应 35k token 重写 |
| 13 | 官方压缩后 | dshlc 见到新的官方压缩卡片（`source.kind=compact-checkpoint`）跳过一拍，同一张卡只跳一次（`cooldown.js`） | 压缩后历史重启，commits 自然失配，rail 重新计时 | 等价 |
| 14 | 诊断 | dshlc 用 `spine-decisions.jsonl` 记 skip reason | 日志 `gate=` + `skipped`/`archive_errors` 计数 | 相当 |

#### 需要吸收的四项（按价值排序）——B6 已全部补齐（2026-11-20，`593b2fc8`）

> 已提交 `593b2fc8`（Scope: G-MN），`make check-upstream` 绿（2055 delta / 335 组）；`mode=on` 仍未在任一节点开启，激活顺序见 §6。

1. **账本与胶囊加 `rewrite`**：每次折叠事件记下「紧接那一请求的 `input_tokens`」（1× 重算量）与
   事件的 saved，并列显示。验收样例直接用 §12.8 的表（31–54k rewrite vs 36–96k saved）。
   **实现**：`LedgerEntry.rewrite_tokens`（`-1` = 该回合没读到用量，`0` 是真实满命中）；折叠时只
   暂存待写账本（`foldLedgerPending`），回合结束由 `recordLiveCompactUsage` 用本回合自己的
   `usageTotals.InputTokens` 落行——「请求路径直接写账本」拿不到这个数，这正是它必须晚写的原因。
   胶囊新增「本轮重算（1×）」行与累计重算行；`ContextSaving.total_rewrite_tokens` 只累加已知
   样本。旧账本行没有该字段，读回是 `0`（显示为满命中），这是唯一的口径妥协。
2. **存档引用保护**：`sweep` 跳过仍被某会话 `foldCommits` 引用的路径（或改成按会话分目录、随会话
   存活期回收）。否则磁盘压力下 sticky 块会因文件消失而无声展开。
   **实现**：`sweepProtected(now, live)`；保护集 = 活会话 `foldCommits` 的存档名 ∪ 各
   `<archive>/commits/*.jsonl` 里保留期内记录的存档名。**两段删除都跳过被保护文件**：宁可在保
   护集撑住时让目录超出 2 GiB 上限（日志 `over_cap=true`，新块因写不进存档而保持原样），也不删
   一个已经写进模型上下文的指针目标。超过保留期的 commit log 随扫描删除。
3. **commits 持久化**：每会话一个 append-only 小文件（一次折叠一行：key/path/bytes），重启恢复并
   只采纳仍能在转录里匹配的 key；成本是一事件 1–2 KB 写入。
   **实现**：`<archive>/commits/<sha256(sessionKey)[:8]>.jsonl`，一行 `{ts,name,bytes}`；`name`
   是存档文件名（`sha256(text)` 截断），这是唯一跨进程稳定的身份——内存 key 是 maphash，种子逐
   进程变化，无法持久化。恢复出的名字在重启后第一次 fold 里按渲染出的存档名匹配（`adopt`），匹配
   上才回到 sticky；匹配不上的在这次 plan 结束时丢弃（已提交块在后续请求里永远是候选，匹配不上只
   可能是转录已不含它）。shadow 不写 commit log（`add` 不持久化），否则以后开 `on` 会把从未折
   叠的块当成已提交。日志 1 MiB 封顶、保留尾部 4000 行，temp+rename 落盘。
4. **失败结果折叠加宽**：允许 `isError` 且足够大的结果折叠，卡片用现有 20/20 行并显式标注「该结果
   标记为失败」；同时把「因 `isError`/多块/非文本而不可折的字节数」计进观测行，rail 才有真相。
   可顺带把 `[stderr]`、`exit status` 这类结构性标记纳入 `looksLikeErrorText`。
   **实现**：`isError` 结果过 `min_block_tokens` 即可折，卡片多一行 `status: 该结果标记为失败
   （is_error）` 并用 20/20 摘录；渲染缓存 key 加 `is_error` 变体（同一文本成功/失败各一张卡）。
   观测行新增 `cand_failed_blocks`、`skip_bytes_error`、`skip_bytes_multipart`、
   `skip_bytes_nontext`；`looksLikeErrorText` 补 `[stderr]`、`exit code`（标记须在行首）。

#### 明确不吸收（dshlc 的做法）

- 会话本体改写（架构不同；代理位置是优势）。
- 结构化抽取卡片（B3，需另一次设计评审）。
- `bigGate=650000` 与 local/cloud 分支（rail 100k 永远到不了）。
- 1k token 长读豁免（会每轮切正在读的结果；我们实测每轮重折 2.54×）。
- 逐行 speaker 标注（我们是逐结果卡片，不是混合摘要）。

#### 我们比 dshlc 强的地方（保持）

- 请求路径内同步折叠：无 wait barrier、无「维护没跑完就让请求通过」的竞态。
- 原文可 `Read`：卡片给绝对路径，模型需要时能取回全文。
- `verifyFold` 结构校验后才上线（I7）。
- 单块指针按内容寻址缓存，热路径 1.3 ms；dshlc 每次都要重写会话本体。

#### 一个不来自 dshlc 的想法：折叠后预热（已实测，否决）

**想法**：折叠请求返回后，代理立刻用同一份折叠 body 发一次 `max_tokens=1` 的请求把缓存写热，
让下一轮真实请求命中 0.1×、用户下一轮的 TTFT 里不含重算。

**实测（2026-11-20，同一真实会话回放，3 次折叠事件）**：预热请求全部满命中（`in` 5–53、
`read` 36,864–65,579，2.7–6.3 s），共 15,721 折算 token（+1.7%）；紧随其后的真实请求
用量与不预热时逐条同构，**零收益**。

**为什么必然为零**：折叠请求自己就付了 1× 重写并把重写后的前缀写进了缓存，下一次真实请求本来
就命中——§12.8 的 on100 臂 5 次折叠之后，紧随请求 5/5 都是满命中（如 `in=4,752 read=36,352`）。
预热只是在同一份已写热的缓存上再付一次 0.1×，且它发生在折叠之后，无法把重写从关键路径上挪走。

**唯一可能有益的变体**是「工具执行期间预折」，把重写从用户等待的回合里挪出去；但这要求提前知道
下一份 body——代理只在客户端发送后才看得到 body，做不到（dshlc 能，是因为它住在宿主里，能看到
surface）。而且边界一旦挪动，1× 重写会付两次。**结论：不做，也不进 B5。**

