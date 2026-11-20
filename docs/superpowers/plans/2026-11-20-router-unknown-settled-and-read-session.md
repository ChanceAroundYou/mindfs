# #86 卡死：unknown 算已收尾 + 启动对账补投影 + Router 读会话（router-unknown-settled-and-read-session）

- 日期：2026-11-20
- 状态：已实现并通过本地门禁；未部署、未重启任何节点
- 上游：无（Router 与工作流运行时是本 fork 自有能力）
- 定制登记：`Scope: G-NJ`
- 上游计划：[Router 收尾死锁](2026-11-19-router-settled-run-livelock.md)（同一条闸门的上一轮修复）

## 背景

用户报任务 #86「看板归档」执行时卡死，要求先分析、合理放宽限制、不确定的先讨论。
随后逐条裁定：

> 1. unkown可以算已经收尾，但是最好是不要出unkown　我们让router有读取会话的权限就好了啊
> 2. 可以
> 3. 给router专门设置一个工具，他有全部读取的权力，但是一般只能读取到最后一个文本，可以通过offset读到前面

## 现场证据（只读复制线上库）

- 上一轮 Router 回合在 11:35 撞 90s 死线，回落旧路径，建 Run `run-61f8d92ace189e15`。
- 该 Run 只有一个 `execute` 节点，NodeRun 在进程重启时被标 `unknown`
  （`startup_ambiguous_side_effect`），但 `workflow_runs.node_states_json` 仍是空。
- brief 因此挂上 `active_execution: {current_nodes: []}`，`validateRouterActions` 永久拒绝
  `run_workflow` / `message_agent`；`deliver_to_active_run` 要 1 个活动节点、`answer_waiting_node`
  要 `waiting_user` 节点，都不适用 —— Router 只剩 say/ask_user。
- 时间线显示 0 个节点（投影没写），回复活动 `act_bde1960c77dbe52e` 从 18:29 起永远 `running`。

## 根因

1. **投影只在节点终态写**。`transitionWorkflowNodeRun` 把 running 改成 unknown 时不写
   `node_states_json`，于是「空投影」被 `workflowRunHasOpenNodes` 当成「刚建出来、driver 还没
   claim 的 Run」，永远未收尾。
2. **unknown 在闸门上没有归宿**。它是「重启时结果不明」，既不该重放（`EngineReadyNodes`），
   也不该被当成终态写库（`workflowNodeStateTerminal`，被取代时要冻结成 cancelled），但它确实
   不需要任何人再等它动 —— 闸门缺第三套判断。
3. **Router 没有读会话的能力**。节点结果写在会话转录里，唯一的读工具 `inspect_run` 只看投影；
   投影空白时它没有任何办法弄清上一轮做了什么，只能反复让用户「先结算 Run」。
4. **回复活动没人结算**。回合的收尾在 agent 那一侧，进程重启把它带走了。

## 修复

### P0 闸门与启动对账

- `workflowNodeStateSettled`（`router_brief_open_nodes.go`）：success/skipped/fail/cancelled/**unknown**
  都算已收尾。文件头写明三套判断的分工：`workflowNodeStateTerminal`（能不能冻结）、
  `EngineReadyNodes`（能不能重放）、本函数（还会不会自己动）。
- `workflowRunHasOpenNodes(run, rows)`：行与投影一起看，优先级 open 行 > 投影 > 已结行 >
  两边都没有=未收尾。行优先保证重试安全（attempt+1 排队时投影还写着上一次的 fail）。
- `buildSituationBrief` 只取一次 NodeRun 行并传给闸门与 `briefRun`；`briefRun` 增加 `unknown`
  桶，并在投影缺节点时用行的最新状态补齐（Loop 内部行、非最高 attempt 跳过）。
- `recoverWorkflowRunsOnStartup`：本轮有 unknown 行、`!wake && !paused` 时调
  `projectWorkflowRunStopped` —— 5 次 revision-CAS，unknown 行写进投影（保留 NodeOutputs），
  其余未收尾节点冻结成 cancelled，落 `node_unknown` 事件；再调 `settleWorkflowReplyUnknown`
  把 running/pending 的回复活动结算成 cancelled 并留一句可读说明。

### P1 Router 读会话工具

- `router_read_session`（Class `RouterToolRead`）：默认读「本任务最近一次节点执行的会话」，
  缺省只给最后一段文本，`offset` 往前翻，`limit` ≤ 5；单块 8KB、整次 16KB 上限。
- 白名单来自 `TaskRelatedSessionKeys`（主会话 + 节点会话 + Run 共享会话 + 活动会话 + 子会话），
  外来 key 一律拒绝；只返回文本块，工具调用/推理/计划块不进 Router 上下文。
- 端口 `RouterTranscriptReader` 由 `api` 层用 `session.Manager.ReadBlocks` 实现，
  `server/app/workspace_build.go` 装配期注入；nil 时工具显式报错，不假装能读。
- brief 的 `PolicyHints` 写明 unknown 的含义与该工具的翻页方式。

## 验收

隔离/本地验证（不碰生产 7331）：

- 新增 15 条测试：`router_brief_settled_test.go`（6）、`workflow_recovery_unknown_settled_test.go`（3）、
  `router_read_session_test.go`（7，含默认最新块、offset 翻页、limit 新到旧、外来会话拒绝、
  未装配端口报错、超大块截断）。
- 五处变异各自打红对应用例：去掉行的参与（只剩投影）/ 把 unknown 从 settled 拿掉 /
  不写投影 / 不结算活动 / offset 不生效。
- `go test ./server/internal/kanban/` 全绿；`go test ./...` 全绿（一次 `TestCoordinatorTickReclaimsStaleClaims`
  的 TempDir 清理偶发失败，属仓库既有 flake，复跑零失败）。
- `go build ./server/...` 成功。

## 残留与待裁定

1. **unknown 会频繁出现的根因没动**：`SessionStateProbeForRoot`（`http_tasks_finish_teardown.go`）
   按策略恒返回 `resumable=false`，所以任何重启只要撞上在飞节点就必定 unknown。要少出 unknown
   需另一次裁定：保持 / 按转录分类成 waiting_user / 只在转录证明无副作用时重排。
2. **「操作员完成时不冻结」**（本轮讨论点 4）：`finishWorkflowRunByOperator` 只取消
   `queued`/`running` 行，`waiting_user` 的节点（human gate / 被挡住的 agent）保持原样。
   任务终态时看不见；「回到待审核」后 Run 仍投影成 `waiting_user`，闸门会挡住新执行，直到那个
   节点被 `answer_waiting_node` 答复或被取代。那是真的停在那里等答复的节点，不是 #86 的幽灵，
   本轮不改。
3. Router 自动 cancel/retry 仍不做（P2 排除）。
