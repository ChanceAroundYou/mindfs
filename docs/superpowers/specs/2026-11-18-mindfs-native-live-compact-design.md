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
| 折叠导致缓存未命中，省钱变亏钱 | 只折窗口左边界；前缀不变量测试；Phase A 记录真实 cache_read/write |
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

