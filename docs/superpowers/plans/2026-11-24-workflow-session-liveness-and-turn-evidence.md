# 工作流会话活跃判定 · 显式 Run 启动事件 · 回合结算的 Run 证据

> Scope IDs: G-QQ（claimed 2026-11-23T20:30Z）

> Plan 2026-11-24。只本地提交：不 push、不部署、不重启任何节点。

## 背景

看板任务 #112（`task_2b2b5ecd1eaf4fe1`）在进程重启后出现三件事同时发生：

1. Worker 会话其实已经恢复并继续执行，最后把活干完（`5591a77a…` 回合 19:18:44 结算，103 次工具调用）；
2. Router 从 17:58 到 19:15 连续产出 17 条 `task_orphaned`（`status=running`、`session_key=''`、`open_nodes=false`），反复询问是否重开；
3. 用户手动在会话里推进的这轮完成（`turn_settled`，`out_of_band=true`），Router 据此写了「完成」总结，但 Workflow Run 投影仍是 `plan=unknown / implement=cancelled`，两边对不上。

只读取证确认这不是一个 UI 文案问题，而是三条互相独立的机制缺口。

## 根因

### R1 任务活跃判定只认 `tasks.main_session_key`

`taskHealthInputFor` 用 `s.sessionRunning(task.MainSessionKey)` 判活，但显式发起的 Workflow Run 把共享会话写在 `workflow_runs.session_key`，从不写 `tasks.main_session_key`（后者是直连路径的字段）。于是：

- Run 会话还在回话时，巡检看到的仍是「没有回复、没有 open 节点、没有活会话」；
- `classifyTaskHealth` 落入 `taskHealthOrphaned`，每 5 分钟一个桶，形成重试询问循环。

`Service.taskRuntimeLive` 已经是「任务此刻是否有人在跑」的唯一判定，`TaskRuntimeSessionKeys` 已聚合主会话、Run 共享会话、节点会话与活动会话。巡检没有复用它。

### R2 显式 Run 的启动对账被 ownership 闸门静默丢弃

`routerEventOriginForRun` 要求 Run 关联的 `task_activities` 有非空 `router_decision_id`，否则 `emitRouterEvent` 直接返回。这是「显式发起的 Run 不属于 Router 生命周期」的既有语义，正常扫描应保持；但启动对账不同：进程重启把正在跑的节点标成 `unknown`、把回复结算成 `cancelled`，这是用户必须知道的事实。现在 `recoverWorkflowRunsOnStartup` 对显式 Run 调 `emitRouterEvent`，被同一个闸门吞掉，Router 完全不知道发生过中断。

### R3 `turn_settled` 不携带 Run 身份与 Run 证据

`SettleSessionTurn` 只看「会话 → 任务」，不知道这条会话也绑定着某个 Run，因此：

- 事件的 `RunID` 为空，brief 走「会话回合不属于任何 Run」分支；
- Router 只看到 `tool_calls=103` 与完成正文，没有任何节点状态；
- 摘要提升只看 `HadToolCalls`，于是「会话里这轮干完了」被当成「Workflow 完成」。

OOB 会话完成是真实事实，不该被压掉；问题是它必须同时告诉 Router：这轮会话绑定哪个 Run、该 Run 哪些节点还没结算，不能据此声称 Workflow 成功。

## 修复

1. **巡检统一判活**：`taskHealthInputFor` 的 `SessionLive` 改为 `s.taskRuntimeLive(ctx, store, task.ID, "")`，并把 `OpenNodes` 保留为独立证据。任一绑定会话（含 Run 共享会话、节点会话、活动会话）在回话，就不再判孤儿。
2. **启动对账专用事件出口**：新增 `emitStartupReconcileRouterEvent`，只在启动对账/取消收尾两处使用；显式 Run 允许越过 ownership 闸门，`UserActivityID` 先取 `run.ActivityID`（必须是本任务的用户留言），取不到再回溯该 Run 创建前的用户留言。正常 `scanRouterEventSourcesBounded` 的 explicit-skip 语义与测试保持不变。
3. **结算事件绑定 Run**：`SettleSessionTurn` 通过 `workflow_runs.session_key` 或 `workflow_node_runs.session_key` 反查 Run，写入事件 `RunID`；brief 对带 Run 的 `turn_settled` 调 `attachRouterEventNodes`，并在 Run 存在未成功节点（unknown / cancelled / running / queued / waiting_user / fail）时追加确定性未验证项：会话回合结算不等于 Workflow 成功。摘要提升仍以 `HadToolCalls` 为准，不机械压掉真实 OOB 产出。

## 测试（先 RED）

- `TestTaskHealthSweepTreatsWorkflowRunSessionAsLive`：`main_session_key=''`、无回复、无 open NodeRun，但 `workflow_runs.session_key` 的探针为活 → 不产 `task_orphaned`。
- `TestStartupReconcileEmitsForExplicitRun`：`triggered_by=user`、无 `router_decision_id`、running 节点 → 恢复后事件账本有 `startup_reconcile`，`RunID` 与 `UserActivityID` 正确。
- `TestTurnSettledCarriesBoundRunEvidence`：会话绑定 Run、Run 有 unknown 节点 → 事件 `RunID` 非空；brief 带节点证据，并出现「会话结算不等于 Workflow 成功」的未验证项。
- 变异验证：把巡检判活退回 `MainSessionKey`、把启动事件出口退回 `emitRouterEvent`、把事件 `RunID` 去掉、把 brief 的 run 分支去掉，各自打红对应用例。

## 执行结果（2026-11-24）

三条用例先跑出预期红：`task_orphaned` 事件照常产出、`turn_settled.RunID=""`、启动对账后事件账本为空。实现后三条全绿，四变异各自打红后还原：

| 变异 | 打红的用例 |
|---|---|
| `live := s.sessionRunning(task.MainSessionKey)` | `TestTaskHealthSweepTreatsWorkflowRunSessionAsLive` |
| 启动对账退回 `emitRouterEvent` | `TestStartupReconcileEmitsForExplicitRun` |
| `turn_settled` 不写 `RunID` | `TestTurnSettledCarriesBoundRunEvidence`（RunID 断言） |
| brief 对 run-bound `turn_settled` 不挂证据 | `TestTurnSettledCarriesBoundRunEvidence`（节点证据断言） |

实现落点：

- `router_task_health.go`：判活改 `taskRuntimeLive`，`OpenNodes` 仍作独立证据。
- 新增 `router_event_startup.go`：只在启动对账/取消收尾使用的 `emitStartupReconcileRouterEvent`；显式 Run 越过 ownership 闸门，来源留言先取 `run.ActivityID`（必须是本任务用户消息），再回溯 Run 创建前的用户留言。正常扫描的 explicit-skip 语义未动。
- `service_turn_settled.go` + `task_store_workflow_runs.go`：`WorkflowRunIDForSession` 按 Run 共享会话 → 节点会话反查，事件与载荷都带 `run_id`。
- 新增 `router_event_turn_evidence.go`：run-bound `turn_settled` 挂节点证据；任一节点非 success/skipped 或没有节点行时追加确定性未验证项。`router_event_brief.go` 的 if/else 链抽到这里，保持 500 行硬线。
- 摘要提升仍只由 `HadToolCalls` 决定，不压掉真实的 OOB 产出。

定向相关用例：`go test ./server/internal/kanban/ -run 'TestTaskHealth|TestTurnSettled|TestStartupRecovery|TestStartupReconcile|TestRouterEvent' -count=1` 全绿（24.5s）；`go build ./...` 成功。全量 kanban 包结果见提交前复跑记录。

## 边界

- 不改 `scanRouterEventSourcesBounded` 对显式 Run 的跳过语义；
- 不把 Run 共享会话写进 `tasks.main_session_key`（现有设计刻意只在读取时回填，写入会改变直连路径语义）；
- 不删除/重写任何 `.mindfs/sessions` 数据；
- 不部署、不重启、不 push。