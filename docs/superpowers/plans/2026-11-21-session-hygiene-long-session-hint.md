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
- `Snapshot` = `Observe(..., false)`：读口（胶囊轮询）不能推进计数。
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

## 状态

**已实现并完成门禁；未部署、未重启任何节点。** 线上行为不变：`config.json` 里没有
`session_hygiene` 段时用默认值（默认开），但要**重启进程**才会加载这段新代码；前端刷新生效。
本机 `sudo systemctl restart mindfs` 仅用户可执行。
