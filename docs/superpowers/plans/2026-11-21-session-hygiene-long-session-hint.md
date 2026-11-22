# 超长会话提示：久 × 大 三档 + N 轮后自动触发 harness 压缩

> Plan 2026-11-21。用户原话：「那我们能不能有一个超长会话的小hint 当一个会话又持续久又内容多的时候，
> 主动提示甚至主动触发一下harness自己的压缩？持续久但压缩过内容不多的短对话不触发、持续不久但是
> 已经很长的胶囊提示、又久又长的几轮提示后自动触发压缩。」
>
> 逐条裁定：默认开、配置可关；Claude 与 Codex 两个 harness 一起做；提示复用上下文胶囊；触发对用户
> **静默自动**；范围「确认，开始执行」。Scope: G-PG。

## 需求与边界

| 维度 | 久（连续活跃） | 大（上下文 token） | 行为 |
|---|---|---|---|
| 又久又大 | ✓ | ✓ | 胶囊提示；再过 `hint_turns` 个用户回合，由服务端自动补一个 harness 原生压缩回合 |
| 只大不久 | — | ✓ | 胶囊提示（含「立即压缩」）；**永不**自动触发 |
| 只久不大 | ✓ | — | 什么都不做：低负载长对话不值得一次摘要请求 |
| 关闭 | — | — | `session_hygiene.enabled=false`（或节点不装配）→ `disabled`，不观测、不触发 |

**不做**（用户明确排除）：不自动新建会话、不归档、不删转录；不改 live-compact 的任何阈值；
「只久」永不自动压缩；不做跨会话面板/统计。

其他边界：
- 触发只发生在**用户回合结束时**（`SendMessage` 正常返回之后），不在回合进行中打断。
- 自动压缩与手动压缩走同一条路：内容固定 `/compact`，由 harness 自己执行。
- 自动压缩失败一次就停手（`auto_failed`），胶囊里写明只能手动；不做指数退避重试。
- 状态在进程内（按 `root + session`），重启后「久」从零重新累计。

## 设计

### 1. 三档是纯函数（`server/internal/api/usecase/session_hygiene.go`）

```go
EvaluateHygiene(cfg HygieneConfig, snap HygieneSnapshot) HygieneView
```

- `Enabled=false` → `disabled`，不看输入。
- `long := activeSeconds >= cfg.ActiveSeconds`，`big := contextTokens >= cfg.ContextTokens`。
- `long && big` → `hint`；`big` → `notice`；否则 `normal`。
- `hint` 档给 `turns_until_auto_compact = max(hintTurns - turnsSinceHint, 0)`；
  `remaining == 0 && !autoFailed` → `auto_compact_due`。
- 同样的输入永远同样的结论 —— 阈值判定与「现在该不该触发」都只在这里算一次，测试直接打这张表。

### 2. 状态机（`HygieneTracker`，同文件）

- 每次观察累计活跃时长：`gap = now - lastSeen`，只有 `0 < gap <= idle_gap` 才计入 —— 空闲超过
  30 分钟的那一段不算「持续久」。`ObserveAt` 收显式时间，测试不睡真时钟。
- `Observe(..., turnEnded bool)`：`turnEnded=true` 表示一个用户回合刚结束。**提示刚出现的那一轮
  不计数**（`hinted` 从 false 翻 true 时 `turnsSinceHint` 仍为 0），用户看到提示时还剩完整的 N 轮。
- `Snapshot` 是完全无副作用的读取：**不推进计数、不置 `hinted`、不置 `autoAttempted`、也不建记录**。
  （2026-11-21 修正：原先它复用 `Observe(..., false)`，在「提示刚成立」的那一格顺手把 `hinted`
  记成 true，紧接着的回合结束就被算成「提示之后第 1 轮」—— 用户看到的 N 轮窗口整体少一轮，
  隔离实例实测出现「提示与自动压缩同一刻发生」。读口在回合中途随时可能被前端刷新触发，
  它一旦改状态，触发点就不再只由回合结束决定。）
- `MarkCompacted`：清 `hinted / turnsSinceHint / autoFailed / autoAttempted`，保留 `activeSeconds`
  —— 压缩解决的是「大」，改不了「久」。
- `MarkAutoFailed`：置 `autoFailed`，之后 `auto_compact_due` 恒 false。
- `AutoCompactDue` 一次性上报：`autoAttempted` 保证同一个「到期」只触发一次，不会被下一次读口重复消费。
- 全部方法 nil-receiver 安全：节点没装配 tracker 时读口照常返回 `disabled`。

### 3. 装配与触发（`server/internal/api/usecase/session_hygiene_wire.go`）

- `hygieneProvider` 可选接口（`SessionHygiene() *HygieneTracker`），`AppContext` 实现它；
  `Service` 只持有 `Registry`，type-assert 取能力 —— 没装配就是「关闭」，不是 panic。
- 观测点两处：
  - 回合**开始**：`observeHygieneTurnStart`（记录 harness、起始时间）。
  - 回合**正常结束**：`session_send.go` 在 `sendErr == nil` 时调 `finishHygieneTurn(..., lastContextWindow.TotalTokens)`；
    取消/失败的回合不算「用户又聊了一轮」。返回 true 即 `scheduleHygieneAutoCompact`。
- 不观测的回合：内容以 `/` 开头的命令回合（含自动压缩自己那一轮）、`hygieneCompact` 标记的回合。
- harness 判定：`hygieneHarnessForSession`（`InferAgentFromSession`，`session.Session` 没有 Agent 字段）；
  只有 `claude` / `codex` 支持本地压缩命令（`codex-acp`、`gemini`、`shell` 都不行）；只对
  `TypeChat`（`""`）会话生效，命令会话 / router 会话 / 文件查看会话一律跳过。
- 自动回合执行体 `runHygieneAutoCompact`：
  1. 无论从哪条路径进来，都强制 `Content="/compact"`、`hygieneCompact=true`；
  2. `waitSessionIdle` 每 2s 轮询 `SessionTurnActive`，等当前回合真正收尾（总预算 30 分钟）；
  3. `sendHygieneCompact` → 生产走 `SendMessage`；测试经 `Service.hygieneSend`（本包唯一测试缝）注入假实现；
  4. 只有看到 `onCompactSeen`（harness 真的发出压缩事件）才算成功 → `markHygieneCompacted`；
     否则/出错 → `MarkAutoFailed`，胶囊显示红色「上次自动压缩没有生效」。
- `SendMessage` 里 `markHygieneCompacted` 挂在既有 `EventTypeCompact` 处理上：**手动与自动**压缩
  都会清状态（自动那一轮若不清，状态会永远停在 hint）。

### 4. 配置（只走 `config.json`）

```json
{
  "session_hygiene": {
    "enabled": true,
    "active_hours": 2,
    "context_tokens": 140000,
    "hint_turns": 3,
    "idle_gap_minutes": 30,
    "harness": { "codex": { "context_tokens": 180000 } }
  }
}
```

- 零值 = **默认值**，不是关闭：`enabled` 用 `*bool` 区分「没写」与「显式 false」；数值 0 一律沿用
  默认（`opts.X > 0` 才覆盖）。默认 `2h / 140k / 3 轮 / 30 分钟`；Codex 默认 180k（两家 CLI 的自动
  压缩都在真实上下文 ~167k 附近触发，留出先提示再自动的余量）。
- `session_hygiene.harness.<name>` 只覆盖写出来的字段，其余沿用全局。
- 解析点在 `cli/cmd/startup.go`（`startupSessionHygieneConfig` → `sessionHygieneOptions`），
  映射在 `server/app/session_hygiene.go`（`sessionHygieneConfigFromOptions`），随
  `StartOptions.SessionHygiene` → `AppContext.Hygiene`。

### 5. 读口与手动入口

- `GET /api/sessions/{key}/context-saving` 的响应新增 `hygiene` 块（`HygieneView` 同名字段：
  `state / enabled / active_seconds / context_tokens / hint_turns / turns_since_hint /
  turns_until_auto_compact / auto_compact_due / auto_failed`）。非 chat 会话或不装配时返回 `disabled`。
- 新增 `POST /api/sessions/{key}/compact`（202 Accepted）：把 `/compact` 回合排出去就返回，结果
  走正常会话事件流；harness 不支持或不是 chat 会话 → 400 并说明原因。
- 前端（复用上下文胶囊，不新开入口）：
  - `visible` 增加 `hygieneBand`（`notice` / `hint`）：一次都没折叠过的会话也要能看到提示。
  - 芯片：没有折叠数据时显示 `上下文 {tokens}`；`hygieneBand` 时变琥珀色；tooltip 分别给
    「还有 N 轮自动压缩」/「可以压缩一次」。
  - 面板新增两行（活跃时长、上下文大小）与 `hint` 档倒计时；`auto_failed` 时红色说明；
    `hygieneBand` 时给「立即压缩」按钮（`compacting` 期间禁用，8000ms 后补一次刷新）。
  - 换会话/卸载时清掉补偿刷新的定时器，别让它打旧会话。

## 实现清单

后端（新增）：`server/internal/api/usecase/session_hygiene.go`、`session_hygiene_wire.go`、
`server/app/session_hygiene.go`；测试 `session_hygiene_test.go`、`session_hygiene_wire_test.go`。

后端（修改）：`server/internal/api/usecase/root.go`（`hygieneSend` 测试缝）、`session.go`
（`SendMessageInput` 的 `hygieneCompact` / `onCompactSeen`）、`session_send.go`（开始观测 + 结束推进 +
压缩事件清状态）、`session_context_saving.go`（读口带 `hygiene`）、`appcontext.go`
（`Hygiene` 字段 + `SessionHygiene()`）、`http_session_context_saving.go`（`handleSessionCompact` +
读口）、`http_routes.go`（`POST .../compact`）、`server/app/server.go`（`SessionHygieneOptions` +
`StartOptions` 字段 + 装配）、`cli/cmd/startup.go`（配置结构 + 解析）、`cli/cmd/mindfs.go`（透传）。

前端（修改）：`web/src/services/agent/contextSaving.ts`（`SessionHygiene` 类型 +
`compactSession` 调用 + `hygiene` 字段）、`web/src/components/agent/useContextSaving.ts`、
`web/src/components/agent/ContextSavingIndicator.tsx`、`web/src/i18n/locales/{zh-CN,en-US}-agents.ts`
（13 个键）、`web/tests/context-saving-chip.test.mjs`。

## 验证

### 单元 / 契约测试

后端 21 条：三档表、`disabled` 短路、提示当轮不计数、N 轮后到期、只有回合结束推进计数、
压缩清状态、空闲间隔不计入活跃时长、会话隔离、失败停手、到期只报一次、harness 覆盖；
`/compact` 只认 claude/codex、只认 chat 会话、斜杠命令不计数、自动回合不参与观测、自动成功/失败
三条路径（无压缩事件 / 发送出错 / 收到压缩事件）、手动入口只排回合不阻塞。

前端 23 条（`context-saving-chip.test.mjs`）：`hygiene` 九个字段与服务端同名、notice 档无倒计时、
hint 档倒计时 + 「立即压缩」+ POST 路由、`auto_failed` 说明、没折叠过也显示胶囊、i18n 两语言成对。

### 变异验证

在干净提交的临时 worktree 里逐条改坏 → 跑定向测试确认打红 → `git checkout` 还原（不污染共享工作区，
也不打扰并发会话的门禁）。记录见文末「变异账本」。

### 门禁

`go build ./...`、`go test -count=1 ./...`、`npx tsc -b --noEmit`、`npx vitest run`（全量）、
`make check-upstream`（本组登记 G-PG）。

### 变异账本

逐条改坏 → 跑定向测试确认打红 → 还原（改坏期间不提交，还原后 `git status` 干净）。

| 编号 | 改坏什么 | 打红的用例 |
|---|---|---|
| M1 | `Snapshot` 退回 `Observe(..., false)`（旧实现） | `TestHygieneSnapshotDoesNotConsumeTheArrivalTurn` |
| M2 | 提示当轮也计数（去掉 `!newlyHinted`） | 7 条 hygiene 用例（含当轮不计数、N 轮到期、压缩清状态） |
| M3 | 三档退化成「只大就提示」（`case big:`） | `TestEvaluateHygieneBands`、`TestHygieneSessionsAreIsolated` |
| M4 | 到期不再「只报一次」（去掉 `autoAttempted` 判定） | `TestHygieneAutoCompactIsReportedOnlyOnce` |
| M5 | 活跃时长不再丢弃空闲间隔 | `TestHygieneActiveTimeIgnoresIdleGaps` |
| M6 | `MarkAutoFailed` 不置位 | `TestHygieneAutoFailureStopsRetries` 等 3 条 |
| M7 | `MarkCompacted` 不清提示（只清轮数） | `TestHygieneCompactionClearsHint` 等 3 条 |
| M8 | 前端去掉回合结束后的延迟刷新 | `context-saving-chip` 的「回合结束才刷新一次」契约 |
| M9 | 胶囊去掉 `data-hygiene` | `context-saving-chip` 的稳定标识契约 |

M7 第一次没打红 —— 原用例靠「压缩后上下文掉回 40000」走到 `normal` 分支顺带清了状态，钉不住
`MarkCompacted` 本身。改成「压缩后上下文仍然很大、直接调用 `MarkCompacted`」后才红。

### 隔离实例端到端验收（2026-11-21）

隔离实例（`scripts/mindfs-iso.sh`，端口 7431、独立配置/账户/项目，线上 7331 全程未碰）里
把阈值压到两轮可见：`session_hygiene = {enabled, active_hours 0.0005, context_tokens 1, hint_turns 1}`，
真实 Claude（`haiku` → 本机别名 `ok`），`e2e/test_session_hygiene.py` 三条断言全过：

- 第二轮结束：`GET /api/sessions/{key}/context-saving` 的 `hygiene.state == "hint"`、
  `turns_until_auto_compact == 1`，且胶囊在 DOM 里（`[data-testid="context-saving-chip"]` 的
  `data-hygiene="hint"`，aria-label 带「自动压缩」）。
- 第三轮结束：服务端日志 `auto_compact.due` → `auto_compact.start` → `auto_compact.done`，
  周期里出现 CLI 自己的 `{"type":"system","status":null,"compact_result":"success"}` ——
  **这是 harness 真的压了，不是我们把状态改成已压缩**；整段只压一次（修读口之前同一段压了两次）。
- 压缩后 `turns_since_hint == 0`、`auto_failed == false`、`turns_until_auto_compact == hint_turns`。

这轮验收还挖出两个真实缺陷，均已先 RED 后修：

1. **读口改状态**（上面 §2 的 `Snapshot`）。修复前实测「第二轮结束就 due、随即压缩」，
   用户等于没有提示窗口；修复后压缩落在第三轮结束。
2. **胶囊不刷新**：界面看到的「回合回落空闲」来自回合内广播，而卫生结论在 `SendMessage`
   收尾时才写，只刷一次实测读到旧值（长时间停在 `normal`，胶囊永不出现）。`useContextSaving`
   在回合结束后补一次 2 s 延迟刷新，用独立定时器（不能和「立即压缩」的补偿刷新共用）。

### 测试实现里踩到的坑（写给下次）

- 无头 Chromium 的 `navigator.language` 是 `en-US`：拿中文 aria-label 找胶囊会永远等不到。
  胶囊补了语言无关的 `data-testid` + `data-hygiene`，e2e 用 `locale="zh-CN"`（与用户环境一致）。
- 主区切换器与引导浮层没有语言无关的选择器：切换器按位置点第 4 格（聊天），引导浮层在
  `add_init_script` 里把 `mindfs-onboarding-state` 标成已看过。
- 「久」是从第一次观测起算的：首轮只有起点没有间隔，所以 `active_seconds` 首轮必为 0，
  hint 最早出现在第二轮结束、自动压缩在第三轮结束（`hint_turns=1` 时）。

## 2026-11-22 追加：胶囊提示瘦身 + 「立即压缩」按钮状态不可信

用户两条新反馈：① 胶囊里的「10 次请求没读到用量，未计入重算合计。」不要再显示；
② 「压缩按钮点击了以后无法生效，codex cc 都不行」。

### 取证（先复现，不改代码）

- 生产 7331 在反馈时段无 `[session-hygiene]` 日志；wsl worker 上有真实点击：
  `23:27:48 POST /compact status=202` → `auto_compact.start` → `23:36:34 auto_compact.done`，
  下一次 `23:36:36 start` → `23:39:56 done`。Claude 真压缩成功，但耗时 3–9 分钟；
  前端原先固定 8 秒就把按钮恢复成「立即压缩」，用户据此判断「没生效」。
- Codex 同节点 `23:33:16 POST /compact status=202` → `auto_compact.error`：
  `codex thread error: Your access token could not be refreshed. Please log out and sign in again.`
  是设备本地凭据过期；旧 UI 只在日志里记原因，胶囊没有具体错误。
- 隔离实例 7432 真浏览器复现同一条链路：POST 202 → `[agent/claude] input content="/compact"` →
  `compact_result=success` → `auto_compact.done`，整段约 34 秒。按钮链路本身能发出请求；
  缺陷是**前端按固定秒数猜结果、服务端又不把真实状态回传**。

### 修复

- 删除 `contextSaving.usageMissing` 组件渲染、zh/en 文案与旧断言；缺失样本数仍只进账本，
  命中率那行继续显示「未知」（提交 `2e41ca10`，Scope: G-MN）。
- `HygieneView` 新增 `compact_pending` / `compact_error`：排程时 `MarkCompactPending`，
  收尾时 `FinishCompact`（成功清空错误，失败写规范化后的原因并停自动重试）；
  手动与自动压缩走同一个收尾闸。`Snapshot` 保持无副作用。
- 前端按服务端 `compact_pending` 控制按钮：点击后只发 POST 并立即刷新一次，
  pending 期间每 2 秒补拉，pending 真变 false 时解除禁用并补刷；
  旧节点没有该字段时留 80 秒兜底，不再用 8 秒猜。
  胶囊在失败时显示 `compact_error`，`hygieneAutoFailed` 文案改为覆盖手动失败。

### 验证

- RED→GREEN：Go 两条用例（snapshot 字段与手动回合生命周期）、web 契约 26 条先写断言后实现；
  全量 `go test -count=1 ./...`（28 包全绿）、`npx tsc -b --noEmit` clean、
  `npx vitest run`（260 文件 / 863 通过 / 6 跳过）全绿。
- 变异账本（每次改坏生产代码 → 跑定向测试 → 必须红 → 还原复绿）：
  后端「pending 不置位 / pending 不清 / 不写 error / 排程不标记」四条各自打红；
  前端「按钮不读 pending / 文案不看 pending / 删错误详情 / 恢复 usageMissing /
  改回 8 秒计时 / 去掉 pending 轮询」六条各自打红。
- 真实复现路径复跑：隔离实例 7432 从工作区重建二进制与前端后，
  `e2e/test_session_hygiene.py` 两条全 PASS（123.21 s）。新增的
  `test_manual_compact_button_follows_server_pending` 用真 Chromium 点击「立即压缩」，
  断言 POST 202、按钮变「正在压缩…」并禁用、读口 `compact_pending` 真→假期间一直禁用、
  `auto_compact.start → done`、无 error。日志对应
  `00:17:46 start` / `00:17:46 POST 202` / `00:18:29 done`。
- Codex 凭据过期不在代码里自动修复（refresh token 是设备本地凭据，不跨设备复制）；
  修复后用户能从胶囊直接看到具体原因并重新登录。

## 2026-11-22 再修：压缩后上下文 stale + 胶囊累计口径简化

用户看完诊断后收窄了范围：

- 不新增「压缩后上下文 ≈ X tokens」这一行，也不需要把 `本轮请求大小 / 本轮节省` 改叫「上次折叠」。
- 累计行只保留一个总量：**不要「估算」二字，不要「最近 200 次折叠」**。
- token 数字不要六位精度：**>= 10k 一律用 `k`，全胶囊所有 token 数字共用同一个格式化函数**。

### 取证结论（只读，已核对）

- 累计节省来自 live-compact 账本最近 200 行，是 `req_tokens_est - forward_tokens_est` 的滚动合计；
  当前会话 API 从 6,129,539 涨到 6,694,018，口径是会话 + agent，不是全局。
- 胶囊的 `latest` 来自最后一次 proxy fold，harness `/compact` 不写账本；所以压缩后
  `本轮请求大小 / 本轮节省` 不变是既有口径，本轮**不动**。
- 真 bug 是 hygiene 的 `context_tokens` stale：Claude compact boundary 原始 JSON 有 `postTokens`
  （当前会话最近一次 manual compact 是 `37,062`，而读口仍是 `142,760`），SDK 结构体没暴露，
  `handleCompactBoundaryMessage` 也没读 raw；`/compact` 回合没有 assistant `message_delta`，
  `UpdateLastContextWindow` 因 `TotalTokens=0` 直接返回，旧值留着，隔离实例复现了压缩后立刻再压一次。
- Codex compact item 没有 post token，只能靠后续 `token_count`；压缩事件后必须先把旧值标 stale，
  不能再拿它判 hygiene，直到新 `token_count` 到达。

### 要做

1. **后端：压缩后 context token 不再 stale**
   - Claude：消息循环把 compact boundary 的 raw JSON 传进 `handleCompactBoundaryMessage`，
     解析 `compactMetadata.postTokens`；`CompactNotice` 增加可选 `post_tokens`；
     `session_send.go` 收到 `EventTypeCompact` 时若 `post_tokens > 0`，用它更新本回合的
     `lastContextWindow.TotalTokens` 并落库。
   - 原始 `pre_tokens` / `post_tokens` 只作为内部状态用，**不在胶囊新增展示行**。
   - Codex：收到任意 compact 事件后把该会话的 context token 标为 stale；stale 期间 `Snapshot`
     不把旧值喂给 hygiene，也不允许再排自动压缩；后续 `token_count` 到达即清 stale 并更新。
   - 目的只有两个：不再重复 `/compact`；成功压缩后不再因 stale 反复进 hint / 失败红块。
2. **前端：统一 token 数字格式**
   - 用一个 `formatTokenAmount` 替换胶囊里现有的 `formatTokens` / `formatTokensEstimate` /
     `formatTokensCompact`：`< 10_000` 原值（保留千分位）；`>= 10_000` 一律换算成 `k`
     （`< 100k` 一位小数并去尾随 `.0`，`>= 100k` 整数 k，例如 `6,129,539 → 6130k`）。
   - 「本轮请求大小 / 本轮节省 / 折叠原文 / 本轮重算 / 本轮缓存命中 / 累计重算 / hygiene 上下文」
     等所有 token 数字都走它；`formatBytes` 仍只用于旧账本缺 token 字段的字节兜底。
3. **前端：累计行只显示总量**
   - `ContextSavingIndicator.tsx` 的累计行不再传 `count`，文案不出现「估算」「最近 200 次折叠」。
   - zh-CN / en-US 的 `contextSaving.savedTotalValue` 改成只接 `{size}`；label 保留「累计节省」。
   - 数据仍取 `total_saved_tokens`（缺样本才退 `total_saved_bytes`），但不再解释估算属性与窗口长度。

### 不做

- 不新增压缩后大小展示行。
- 不改「本轮请求大小 / 本轮节省」两行的语义与文案。
- 不改账本保留 200 行的策略，不改 API 字段。
- 不在本轮处理用户未复现到的红色胶囊瞬时状态；修 stale context 后若仍能复现，另开一轮取证。

### 测试与变异

- Go：`postTokens` 原始 JSON 解析、compact 事件更新 `lastContextWindow`、Codex stale 闸各一条 RED→GREEN；
  E2E `e2e/test_session_hygiene.py` 增加「压缩后 `context_tokens` 下降且只压一次」断言。
- Web：`web/tests/context-saving-chip.test.mjs` 钉住三件事——统一格式阈值（9,999 原值 / 10,000 → 10k /
  6,129,539 → 6130k）、所有 token 数字共用同一个 formatter、累计行不含 `估算` / `200` / `count`。
- 变异各自打红后还原：去掉 `postTokens` 解析、去掉 Codex stale 闸、累计行恢复 `count`、
  阈值退回 1k、任一 token 行改回旧精确格式化。
- 门禁：`go test -count=1 ./...`、`npx tsc -b --noEmit`、`npx vitest run`、
  隔离实例真 harness E2E、`make check-upstream`；提交标 `Scope: G-PG`，并同步 G-PG 定制登记。

### 实施结果（2026-11-22）

- **Claude 压缩后大小**：`CompactNotice` 增加 `PostTokens`；`session_compact_tokens.go` 按 compact boundary 的 UUID 从 CLI transcript JSONL 补读 `compactMetadata.postTokens`（同时兼容 snake_case），`handleCompactBoundaryMessage` 写进事件；`session_send.go` 把它写回 `lastContextWindow`，`contextWindowAfterCompact` 保证随后到达的旧 `MessageDone` 不覆盖。转录读不到时退回原值，不拿估算冒充真实值。
- **Codex / 无 postTokens**：压缩事件先到时 `HygieneTracker.MarkContextStale`，`Observe` 在该窗口内返回带旧 context 的视图但强制 `AutoCompactDue=false`、不推进轮数；Claude 拿到 postTokens 或 Codex 后续 `token_count` 到达时清 stale。状态只在进程内，重启后清空。
- **胶囊**：新增 `formatTokenAmount`，所有 token 数字 <10k 原值（千分位）、>=10k 用 k；累计行只用 `savedTotalValue` 传一个总量，不写「估算」「最近 200 次折叠」；「本轮请求大小 / 本轮节省」保持原样；没有新增「压缩后上下文」展示行。
- **RED→GREEN**：2 条 Claude 转录读取、1 条 stale 闸、2 条上下文合并、2 条 formatter、26 条胶囊契约；先看见 `PostTokens undefined` / `MarkContextStale undefined` / `contextWindowAfterCompact undefined` / 模块缺失与旧文案断言失败，再实现到全绿。
- **变异验证（6 处各自打红后还原）**：去 transcript postTokens、去 stale 闸、context 合并不覆盖、累计行恢复 count、formatter 阈值退回 1k、请求行改回精确整数。
- **门禁（干净 worktree，只叠本组改动）**：`go test -count=1 ./...` 全包绿；`tsc -b --noEmit` clean；`npx vitest run` 261 文件 / 865 通过 / 6 跳过；`gofmt -l` 无输出；`make check-upstream` 2358 delta / 358 组全绿（含 G-PG 新锚点）。隔离 E2E 本轮未重跑，真实 harness 压缩仍由上一轮证据覆盖。
- **第三轮（2026-11-22，压缩成功但流尾部报错）**：pc/WSL 的 `HBS-research` codex 会话实测：手动 `POST /compact` 后 Codex rollout 已落 `compacted`，MindFS `.aux.jsonl` 也写了 `compact.status=complete`，但同一回合 `SendMessage` 返回 `codex response stream ended unexpectedly`。`runHygieneAutoCompact` 原本先判 `err` 再判 `onCompactSeen`，失败分支的 `MarkAutoFailed` 覆盖了 `MarkCompacted` 的清理，于是胶囊长期显示「上次压缩没有生效」。改成：观察过压缩事件后即使流尾部报错也按成功收尾，只写 `reason=stream_error_after_compact` 诊断日志；没有压缩事件才保留原失败提示。新增 1 条用例，RED→GREEN，变异（`if compacted` 改成 `if false && compacted`）打红后还原。边界：不从 rollout 回填 token，post-compact `token_count` 没进事件流时 `context_tokens` 仍可能显示旧值，stale 闸只防重复自动压缩。

## 状态

**前一轮已实现、门禁绿、隔离实例端到端跑通真实 harness 压缩；未部署、未重启任何节点。**
线上行为不变：`config.json` 里没有 `session_hygiene` 段时用默认值（默认开），但要**重启进程**
才会加载这段新代码；新增 `compact_pending` / `compact_error` 的后端字段同样要重启才下发
（旧节点下前端走 80 秒兜底）。前端刷新生效。本机 `sudo systemctl restart mindfs` 仅用户可执行。
端到端证据与变异账本见上；`e2e/test_session_hygiene.py` 刻意不进常规门禁（会真调模型）。

**2026-11-22 第三轮（流尾部异常误报压缩失败）已实现并经定向验证；本轮未部署、未重启任何节点。**

**2026-11-22 四端部署**：`587af6fa`（压缩按钮跟随服务端 `compact_pending`）与 `b913f855`（压缩后
上下文 / 胶囊累计口径）随 `v0.5.5-1201-57-g0cc70c6e` 发布，四端版本逐字一致；本机由用户重启，
运行进程与安装文件同 md5 `540a0e8f…`、`/mindfs/health` 200。更晚的 `045680dc`（流尾异常不再误报
失败）**不在**这次发布里，仍需下一轮 ship。

## 2026-11-22 追加：活跃时间跨重启，并从上次 harness compact 起算

用户报「live compact 的活跃时间总是非常低」，要求改成「从上次 harness 内置 compact 后的累计
活跃时间」。取证发现两个叠加原因：① `activeSeconds` 只在 `HygieneTracker` 进程内累计，服务重启
（含 deploy 后的重启）立刻归零；② 即使不重启，旧语义是「进程启动以来按观测间隔累计」，
`MarkCompacted` 原本保留它，按字面改成「压缩后重新计时」反而会让刚压缩完的数值更小。用户裁定
「跨重启保留，从上次 compact 起累计」。

### 改动

- 新增 `server/internal/api/usecase/session_hygiene_store.go`：按 `rootID + "\x00" + sessionKey`
  的 sha256 前 12 位命名 `<configDir>/session-hygiene/hy-*.json`，只存 `version` / `scope` /
  `active_seconds` / `last_seen`。临时文件 + `os.Rename` 原子替换，目录 0700、文件 0600；
  坏文件跳过，写盘失败只记日志，不阻断会话发送。
- `NewHygieneTrackerAt(configDir, cfg)` 启动时回填这两项；`ObserveAt` 每次更新后写盘；跨进程
  的 idle gap 仍按 `IdleGapSeconds` 整段丢弃。
- `markCompactedAt` 在任何 harness 压缩事件后把 `activeSeconds=0`、`lastSeen=now` 并立即写盘，
  于是「久」表示距上次压缩的累计活跃时长；提示轮数、`hinted`、`auto_failed`、
  `compact_pending/error`、`context_stale` 仍只在进程内。
- `server/app/workspace_build.go` 按账户把 `configDir` 接进构造函数，主账户与其余账户各自一份。
- 旧用例 `TestHygieneCompactionClearsHint` 按新语义改为「压缩后先回 `notice`，重新积累够久才
  再次 `hint`」；它同时钉住「提示清零不依赖上下文掉出大档」。

### 验证

- 新增 5 条持久化用例：跨重启累计、压缩后跨重启从零累计、压缩立即写盘、压缩重设 idle 锚点、
  跨重启丢弃长 idle gap。RED（`undefined: NewHygieneTrackerAt`）→ GREEN 全绿。
- 变异验证（各自打红后还原）：去掉 `ObserveAt` 写盘、去掉 compact 时 `activeSeconds=0`、
  去掉 compact 时 `lastSeen=at`、去掉 compact 写盘、去掉启动时 `store.load()`。
- `go build ./...` 通过；`go test -count=1 ./...` 首轮 kanban 偶发红，原样重跑全绿（已知
  sqlite/TempDir flake，见记忆 `kanban-gate-flaky-sqlite-io`）。
- 本轮不部署、不重启任何节点；前端无改动。`045680dc` 已随 `v0.5.5-1201-67-ga48e3684`
  四端发布上线（以 `docs/plans/README.md` 的 2026-11-22 部署状态为权威），本段旧记录说它
  「仍需下一轮 ship」已被那次发布取代。
