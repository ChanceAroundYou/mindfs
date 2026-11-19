# Router 收尾死锁：已结算的 Run 不再算「活动执行」（router-settled-run-livelock）

- 日期：2026-11-19
- 状态：实现完成、包级门禁通过；**未提交 / 未部署 / 本机待用户重启**
- 上游：无（Router 是本 fork 自有能力）
- 定制登记：`Scope: G-MZ`

## 背景

用户在任务 #88「收尾」上连续发了两条留言，Router 都只回「请先结算 Run / 批准或驳回」，
而界面上既没有「结算 Run」按钮，也没有待批的审批卡。用户原话：

> 这个权限的限制有点古怪　你应该先排查相关问题，我是在正常交互的　不应该被卡死。
> 我也不知道什么是结算Run　但我认为这些应该你作为router应该内部处理好的

## 现场证据（只读复制线上库）

- 任务 `task_477ad2273fc64a2a`（「收尾」）`status=waiting_user`，`updated_at 11:54Z`。
- Run `run-d3ef85eed889c18f`（`workflow-new-feature:v1` rev 6，`input→plan→implement→verify(human_gate)`）：
  4 个节点全 `success`，`ProjectStatusFromRun = success`，`waiting_user` 节点 0 个。
- `workflow_run_events`：seq 13 `human_waiting`、seq 14 `human_approved`，之后没有任何
  `run_completed`（本轮修复不依赖它；Run 的终态是从节点投影出来的）。
- 09:06 用户批准了末端 gate；11:36 / 11:53 两条留言都拿到了 Router 的 `say` 回复。

## 根因

1. `taskStatusFromRun` 把「全部节点 success」的 Run 投影成 `waiting_user`（待审核，用户点「完成」）。
2. `buildSituationBrief` 用 `isActiveTaskStatus` 判活动执行，而 `waiting_user` 属于「活动」。
3. 于是 brief 对已结算的 Run 挂上 `active_execution: {kind: workflow, status: waiting_user,
   current_nodes: []}`。
4. `validateRouterActions` 在 `ActiveExecution != nil` 时硬拒 `router_run_workflow` 与
   `router_message_agent`；`deliver_to_active_run` 要求恰好 1 个活动节点（0 个）；
   `answer_waiting_node` 要求有 `waiting_user` 节点（没有）。Router 只剩 `router_say`。
5. 后端本身并不拦下一轮：`PrepareNewUserTurn` 对 `ProjectStatusFromRun == success` 显式
   重置成 `pending` 并放行。拦住的只有 Router 自己的 brief。

即「界面没有入口」不是展示缺失，而是 brief 把已经结束的 Run 谎报成仍在执行。

## 修复

- 新增 `workflowRunHasOpenNodes(run)`：图里只要还有一个节点状态不在
  {success, skipped, fail, cancelled} 就算未结算；`NodeStates` 为空（刚建、投影还没写）
  也算未结算。
- `buildSituationBrief` 只在 `isActiveTaskStatus(status) && (status == StatusPaused ||
  workflowRunHasOpenNodes(run))` 时挂 `ActiveExecution`。
  - 暂停是操作员状态，不受节点是否收齐影响，照旧挡新执行。
  - gate 停在 `waiting_user`、agent 节点停住、节点在跑、刚建出来 —— 全部照旧是活动执行。
- 不改校验层与动作语义：`ActiveExecution` 的含义收窄为「真有可能变化的执行」。

## 验证

- 新增 `server/internal/kanban/router_brief_active_execution_test.go` 四条：
  ① #88 形状（全 success + 任务 waiting_user）→ 无 `ActiveExecution`、`run_workflow` 被接受；
  ② gate 停在 waiting_user → 仍有 `ActiveExecution`、`run_workflow` 仍被拒；
  ③ 未写投影的新 Run + 任务 running → 仍是活动执行；
  ④ 暂停任务 + 全 success → 仍是活动执行、`run_workflow` 仍被拒。
- 变异验证：去掉开放节点闸门 → ① 红；把 `waiting_user` 节点当已收尾 → ② 红；
  去掉暂停豁免 → ④ 红（各自只打红对应用例，还原后全绿）。
- 用线上任务库的**只读副本**（复制到 t.TempDir 再 `NewTaskStore`）跑一次修复后的
  `buildSituationBrief`：#88 的 brief 现在 `active_execution=<nil>`、`run_workflow` 被接受、
  `latest_run` 仍是 `waiting_user` / `success:4` / `waiting_user_nodes: []`（临时测试跑完即删）。
- `go test -count=1 ./server/internal/kanban/` 全绿。

## 未做 / 残留

- 未提交、未部署；部署后 #88 不需要手动「结算」，用户的下一条留言就会正常被 Router
  路由（message_agent / run_workflow 均可）。
- `latest_run.status` 仍沿用任务级口径（`waiting_user`），没有改成 Run 投影的 `success`；
  提示词规则 3 以 `waiting_user_nodes` 为准，不受影响。
- `unknown` 状态的节点仍算开放：那需要操作员在运行视图里 retry/resume，与 gate 同类。
- 用户提到的另外三件事（提交修改、核查任务卡片 CLI 与其他运行节点、把 HTTP 请求拼装做成
  router 专属 skill）不在本轮范围。
