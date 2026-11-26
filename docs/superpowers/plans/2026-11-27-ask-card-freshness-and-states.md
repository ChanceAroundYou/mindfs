# 提问卡可见性：看板即时刷新 · 等待/已答复/已关闭状态一致

> Scope IDs: G-SD（claimed 2026-11-26T20:28Z）

任务 #128 后续轮。上一轮修复（`116d3ae9`，G-RI）解决的是 Router 动作被整轮拒绝；
用户重启到 v0.5.6 后反馈 ask 仍有延迟、状态变化不清楚。本轮不再套用上一个根因，
从重启后的任务库与会话转录重新取证。

## 现场证据（重启后，只读）

- `router_decisions` 自 2026-11-26T18:11Z 起 12+ 条全部 `applied`，无 `silent must be
  the only action` 拒绝：动作不再丢失。
- ask 决策的 `created_at`（触发）到 `updated_at`（应用/落卡）相差 13–28 s，与 Router
  LLM 回合时长一致；这段时间内卡片没有任何中间状态，用户看到的就是「没反应」。
- 同期没有 worker 透传 ask 样本，worker 路径的结论来自代码与既有测试，不冒充实跑。

## 根因

1. **看板卡片只认 `task.updated`**：`task.activity.created/updated` 只刷新详情时间线。
   Router ask 的应用路径只广播活动事件，`markTurnTaskWaitingUser` 在任务已是
   `waiting_user` 时又提前返回、`turn_settled` 触发的 ask 根本不经过它 —— 所以
   `router_ask` 徽标要等下一次无关的任务更新才出现或消失。worker ask 走
   `UpdateTaskAuxFlags` 会广播，于是两个入口表现不一致。
2. **worker ask 在回合结束时只收卡不清位**：`CloseOpenTaskQuestions` 把活动行置
   `cancelled`，任务行的 `aux_flags.ask_user_waiting` 仍为 true，「等你回答」徽标常驻。
3. **UI 没有回答后的状态**：卡答完仍是红色等待外观；被新提问取代/回合结束收口的卡
   与仍在等待的卡长得一样，用户无法判断这张卡是否还需要处理。

## 设计

| 位置 | 改动 |
|---|---|
| `ActivityService.SetTaskCardNotifier` / `notifyTaskCard` | 活动服务新增「任务卡内容变了」端口；apply/answer/close 三处提问生命周期各触发一次 |
| `Service.NotifyTaskCardChanged` | 装配期注入：读详情后 `Runner.TaskUpdated`，与 `UpdateTaskAuxFlags` 同一条 `task.updated` 通道 |
| `ActivityService.CloseOpenTaskQuestions` | 收口后按受影响的 task 逐张通知，不依赖下一次事件 |
| `AppContext.closeTaskQuestions` | `closed > 0` 时清 `ask_user_waiting`；清位本身也会广播 |
| `AskUserQuestionCard` | 新增 waiting / answered / closed 三态胶囊与统一配色；已答复折叠态显示答案摘要，已关闭显示收口原因 |
| 新增 `askUserCardState.ts` | 状态判据、配色、答案摘要抽成纯函数，组件保持在 500 行硬线内 |
| `TaskQuestionCard` | 把活动的 `waiting_user` / `cancelled` / `failed` / `completed` 映射到同一套三态，并传 `summary` 作收口原因 |

## 验证

- Go：apply ask 会通知任务卡；回答与收口都会通知；回合终结收口后 aux 标志归零。
- Web：三态胶囊、答案摘要、收口原因渲染；既有 Router 提问卡与卡片徽标测试不回归。
- 全量 `go test ./...`、web `typecheck` + 全量 vitest、`check-upstream`。

## 边界

- 只改 ask 链路的状态与刷新，不重构看板/活动模型。
- Router LLM 的 13–28 s 是模型回合时长，不在本次代码范围内；本次让这期间的状态
  可见、并在落卡/收口时立即刷新。
- 未 push、未部署；本机重启命令交用户执行（agent 位于 `mindfs.service` cgroup）。