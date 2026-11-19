# 一轮失败不封板 + 终态任务可回到待审核

2026-11-19，用户直接授权（原话）：「你的这个任务本身失败了 且无法继续对话和操作，请修改一下
完成、取消状态的任务也可以回到待审核的状态 加上一个合适按钮，并且不要把一次失败的任务就扔进取消，
这个逻辑不对」。Scope: G-NC。

## 现场

任务 #91 跑 workflow，`execute` 节点撞上游 502（insufficient credits）。Run 投影 fail →
`taskStatusFromRun` 直接写任务行 `fail`（终态），于是：详情面板输入框禁用、提示「任务已结束，
无法继续留言」；卡片落进「失败·取消」列；唯一出路是删卡重建。反方向同样缺：点早了「完成」
或误点「取消」之后没有任何入口回到对话。

## 根因

1. **「一轮的结果」被当成了「任务的结论」**。`taskStatusFromRun` 把 Run 投影原样写进任务行，
   而 Run 的 fail 语义是「这一轮没跑成」，不是「这个任务结束了」。
2. **终态不可逆**：没有任何 API / 服务层动作能改回 `waiting_user`。
3. 即使改回任务行，`PrepareNewUserTurn` 只对 success 的旧 Run 放行下一轮，fail / cancelled
   会被当成「取代旧轮次」冻结；`briefTaskStatus` 又会把旧 Run 的终态投影报给 Router，
   apply 闸门会以「任务已结束」拒掉新留言。

## 改动

| 层 | 文件 | 内容 |
|---|---|---|
| 投影 | `workflow_engine.go` | `taskStatusFromRun`：Run 投影 success / fail 一律落 `waiting_user`；success / fail 只由显式动作写入。`ProjectStatusFromRun` 不动（Run 级失败仍是终态，回复结算与绑定闸门照旧） |
| 生命周期 | `service_user_turn.go` | `PrepareNewUserTurn` 对任何终态 Run（success / fail / cancelled）按「上一轮」放行，不冻结成 cancelled、不改写旧回复 |
| brief | `router_brief.go` | 投影是终态而任务行不是时采信任务行，否则 apply 闸门拿旧结论拒新留言 |
| 新动作 | `task_store.go` / `service_run_moves.go` / `http_tasks.go` / `http_routes.go` | `POST /api/tasks/{id}/reopen` → `Service.Reopen`：终态改回 waiting_user、清 `completed_at`、写 `reopened` 事件、广播；非终态幂等 |
| 前端 | `crud.ts` / `useTaskDetailPanel.ts` / `TaskDetailPanel.tsx` / `TaskCardRows.tsx` / `taskBoardTypes.ts` / `taskIcons.tsx` / `useTaskBoard.ts` / 两份 i18n | 详情面板头部与卡片终态分支各一个「回到待审核」键（回退箭头图标）；卡片动作走 `moveTask(..., "reopen")` 后重拉看板（换列，`applyTaskDetails` 搬不动卡片）；禁用提示改为指路「点上方『回到待审核』」 |

失败仍可见：回复行照旧 `failed` 并带原因（按 Run 投影结算），NodeRun 输出、账本事件原样保留。

## 验证

- 新增 `service_reopen_test.go` 5 条：失败轮落待审核且下一句能开新 Run（旧 Run / 旧回复不变）、
  终态三种状态重开（含结果摘要保留、`reopened` 事件、广播）、非终态幂等、取消任务重开后能发消息、
  brief 采信重开后的任务行。
- 既有契约按新语义更新：`TestTaskStatusFromRunLeavesFinishedWorkInReview`（fail → waiting_user）、
  `TestService_ProjectWorkflowTaskStatusSettlesRouterReply`（fail 用例任务行现在 waiting_user，
  回复仍 failed）、`TestWorkflowNodeAttemptFailingRunnerProjectsFail`（任务行 waiting_user）、
  `web/tests/task-button-gates.test.mjs`（新增卡片/面板 gate 矩阵与接线）、
  `task-card-parity.test.mjs`（终态键顺序改为按位置比较，允许中间夹其它终态键）、
  `workspace-board.test.mjs`（reopen 触发结构重拉）。
- 门禁（干净 worktree `/tmp/mindfs-reopen`，只含本组改动）：`go build ./...`、定向用例全绿；
  `go test -count=1 ./...` 28 包中仅 `TestCoordinatorCrashReplayCompletesWithoutModelCall` 全量负载下
  偶发红一次（单独 `-count=3` 全绿，与本组无关的既有 flaky）；`npx tsc -b --noEmit` clean；
  `npx vitest run` 241 文件 / 708 通过 / 6 跳过；`make check-upstream` 见提交后对账。

## 残留

- 本机 `sudo systemctl restart mindfs` 仅用户可执行；未部署到任何节点。
- 「失败」状态词与「失败·取消」列仍在：显式动作与存量数据照旧渲染；本组只保证一轮失败不再自动进入。
- 重开不重跑：下一轮由用户留言触发，旧 Run 作为证据保留，不新建 Run、不清历史。
