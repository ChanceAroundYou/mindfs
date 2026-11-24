# 上下文溢出压缩成功后的自动重发（复用原回合，有界一次）

> Scope IDs: G-QT（claimed 2026-11-24T06:47Z）

> Plan 2026-11-24。只本地提交：不 push、不部署、不重启任何节点。

## 背景

2026-11-24 线上复现确认：同一个长会话第一次发送必失败，报
`Prompt is too long（terminal_reason=blocking_limit）`，随后（`G-QO`）补了一次有界
`/compact`，界面提示「已开始压缩，完成后请重新发送」—— 用户必须手动再发一次。

根因是两条事实叠在一起：

1. **MindFS 把 Claude 钉在 200k**。`session_models_helpers.go` 只在模型名带 `[1m]` 时
   放开 1M，否则设 `CLAUDE_CODE_DISABLE_1M_CONTEXT`。llmux 把 `os` 宣传成 1,000,000，
   CLI 实际按 `contextWindow: 200000 / maxOutputTokens: 32000` 判，可用发送上限约 168k。
2. 用户裁定**不改 1M、保持 200k 默认**，改走「压缩成功后由 MindFS 自动重发一次」。

用户指令：「我不同意改成1m 我希望默认用200k 请走B」。

## 设计

### 状态归属

待办重发挂在 `HygieneTracker` 的每会话记录上（`hygieneScope(rootID, sessionKey)`），
字段是一份 `OverflowResumeSpec`。选择理由：溢出压缩本来就由 tracker 的
`compactPending` 记账，成功/失败的唯一收口点是 `finishHygieneCompact`；把待办放在同一
处，消费条件与压缩结论天然同源。

**只在内存**：压缩期间 MindFS 重启会丢掉待办，用户看到旧错误、手动重发即可 —— 与今天
一致，不新增持久化面。

### 生命周期（五个判据）

1. `ensureOverflowCompact` 判定「这一轮确实是终端溢出、且压缩在途（刚排上或原本就在跑）」
   时登记待办，内容取自**这条失败的回合**（content / model / mode / effort /
   fastService / planMode / clientCtx / runtimeRoot / turnSeq / turnID）。
2. `SendMessage` 起手：任何**用户发起**的回合（`!hygieneCompact && !overflowResume`）
   清掉待办 —— 用户自己又说了话，重发旧内容已经没有意义。
3. `finishHygieneCompact`：
   - 失败 → 丢弃待办（不重发）。
   - 成功 → 若会话此刻空闲、且队列里没有别的消息 → 交给 Registry 的可选能力
     `ResumeSessionOverflowTurn(spec)` 排进会话队列；否则丢弃。
4. 重发回合 `OverflowResume=true`：`ensureOverflowCompact` 直接返回 false，
   **不会再排压缩、也不会再产生待办** —— 一次为限，压完仍溢出就按普通终端失败收尾。
5. 重发复用原回合身份：`SendMessageInput.ReuseUserTurn={Seq,TurnID}`，跳过
   `persistUserTurnExchange`（不新增 user 行），WS 侧 OnStart 不广播 `user_message`
   气泡、改用 `SetSessionPendingReply` 续上同一个 turnID。用户看到的是「原消息 + 继续
   回复」，不是同一条消息出现两遍。

### 为什么必须异步

`SendMessage` 整个回合持 `getSessionSendLock(key)`，压缩本身也是一次 `SendMessage`。
在失败的调用里同步等压缩再重发会直接死锁；因此重发走会话队列，由回合结束时
`startNextQueuedSessionMessage` 拉起（这也是「用户已经排在前面」时排序正确的唯一办法）。

### 为什么重发要经过 API 层

`SendMessage` 的实时 UI（正文/工具卡/子会话/任务状态跟随）全部由 WS handler 装配。
后台回合若绕过它，用户会看到一个不动的界面。因此 Registry（`*api.AppContext`）实现
可选能力，把 spec 变成一次普通的 `sessionMessageJob` 提交。

## 任务

| # | 内容 | 测试 |
|---|---|---|
| T1 | tracker：`MarkOverflowResume` / `TakeOverflowResume` / `DropOverflowResume` / `HasOverflowResume` | 登记-取出-再取为空；不同会话互不串 |
| T2 | `ensureOverflowCompact` 登记待办 + `OverflowResume` 递归闸门 | 终端溢出登记 spec（含原 content/seq/turnID）；`OverflowResume` 回合不登记、不排压缩 |
| T3 | `SendMessage` 用户回合清待办 | 用户新回合后待办为空；`hygieneCompact` / `OverflowResume` 不清 |
| T4 | `finishHygieneCompact` 消费/丢弃 | 成功 + 空闲 → runner 收到一次；失败 → 丢弃；忙（有活跃回合）→ 丢弃 |
| T5 | API 层：`sessionMessageJob.OverflowResume` + `ReuseUserTurn`、OnStart 分支、队列闸门 | 复用身份时不再落 user 行；OnStart 不广播 user_message；有待排队消息时不提交 |

## 验收

- 每个任务先写测试（RED）再实现（GREEN）。
- 每个任务至少一处变异，必须因**正确原因**变红（断言失败，不是编译失败）。
- 定向：`go test -count=1 ./server/internal/api/usecase/ ./server/internal/api/`。
- 提交信息标 `Scope: G-QT`；登记 `docs/upstream-customizations.yaml` 并跑
  `make check-upstream`。

## 边界

- 不做 1M 窗口、不改 `os` 模型别名。
- 不新增「按大小自动压缩」策略：重发只消费已经因为撞墙而排上的那一次压缩。
- 不在压缩失败时重发、不在重发再次溢出时二次压缩。
- 重启丢待办（内存态），文档写明。