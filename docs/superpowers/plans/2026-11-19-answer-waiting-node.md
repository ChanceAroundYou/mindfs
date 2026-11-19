# 用户答复回填停住的 Agent 节点（answer-waiting-node）

- 日期：2026-11-19
- 状态：已实现并提交（未 push / 未部署 / 本机待用户重启），执行记录见文末
- 上游：`dad415bf`（`Scope: G-MR`，修掉「已转交」假动作）
- 定制登记：`Scope: G-MT`

## 背景

Task #86（`task_2ea72d70e560f8e3`）的 `input` 节点以 blocked 停在 `waiting_user`，
用户回「我希望是A」，Router 只回了一句「（已转交，无其他动作。）」，节点没有任何动作。

`dad415bf` 让 Router **不再撒谎**：`router_deliver_to_active_run` 在这种形态下
当轮就报错（「当前没有正在执行的 Agent 节点…重试该节点才会把你的答复带进去」），
模型于是改口成 say/ask_user，既不会静默丢留言，也不会触发 `fallbackRoute` 冻结整条 Run。

但缺口仍在：**系统没有任何路径能把这段答复真正送进那个停住的节点**。
本轮补的就是这条路径。

### 为什么现有动词都不够

| 现有路径 | 为什么不行 |
|---|---|
| `deliver_to_active_run` → `continue_requests` | 投递只在节点**回合结束时**由 `reconcileWorkflowCommentsAfterNodeRun` 消费（且要求 `NodeRunSuccess`）。`waiting_user` 节点没有在跑的回合，永远等不到投递。 |
| `POST /api/tasks/{id}/workflow-run/nodes/{id}/retry` → `CreateWorkflowNodeAttempt` | 能重开 `waiting_user` 节点，但**不带消息**；前端也没有入口，用户无法把答复塞进去。 |
| `SendWorkflowComment` | 能把消息送进节点会话，但只作为普通续轮，不产生新的 NodeRun/终态/投影 —— 节点仍停在 `waiting_user`，Run 也不会继续往下走。 |

结论：缺的是「**retry with message**」——第 N+1 次尝试，复用原会话，把用户答复
显式拼进下一次节点 prompt，并正常走节点生命周期（worktree、envelope、终态、投影、下游推进）。

## 设计

### 1. 新动作 / 新工具

- `RouterActionAnswerWaitingNode RouterActionKind = "answer_waiting_node"`
- 工具 `router_answer_waiting_node(body, node_id?, reason?)`
- `RouterAction` 新增 `NodeRunID string`：工具在当轮把唯一目标解析成具体 NodeRun ID，
  apply 阶段不再重新解析（状态可能在回合之间变化）。

工具和 `deliver` 一样是**写入动作**：只记录 `{"status":"recorded"}`，apply 在回合末统一执行。

### 2. 工具级前置闸门（当轮报错，而不是回合末 rejected）

工具错误会直接回给模型（`session_open.go` 的 `ErrorResult` 分支），模型当轮就能改口。
回合末 rejected 会触发 `fallbackRoute`，而旧路径会 supersede 整条 Run —— 正是 `dad415bf`
修掉的伤害，所以闸门必须在工具里。

判据（按顺序）：

1. 运行期事件回合：**不拦**（返回 nil）。事件回合本来就由 `validateRuntimeEventActions`
   白名单拒绝，报成「这一轮没有目标」会把白名单的明确拒绝掩盖掉（与 `deliver` 一致）。
2. 有 `running` 的 Agent 节点 → 报错：「还有 N 个节点正在执行，先等这一轮结束再投递」。
3. 有 `human_gate` 停在 `waiting_user` → 报错：「正在等待你审批，普通留言不能替代批准/驳回」。
4. 统计本 Run 内 `waiting_user` 的**顶层 Agent** NodeRun：
   - 0 个 → 报错：「没有停住等待你答复的节点」；
   - 多个且没给 `node_id` → 报错并列出候选 node id；
   - 给了 `node_id` 但不在候选里 → 报错并列出候选；
   - 恰好 1 个（或指定命中）→ 记录动作。

### 3. 服务动词 `Service.AnswerWaitingWorkflowNode`

```go
func (s *Service) AnswerWaitingWorkflowNode(
    ctx context.Context, rootID, taskID, nodeRunID, body, activityID string,
) (NodeRun, error)
```

步骤：

1. `loadForMove` + `workflowRunForTask`（与 `CreateWorkflowNodeAttempt` 同源）。
2. 解析目标：`nodeRunID` 为空时取本 Run 唯一 `waiting_user` 顶层 Agent 行；0/多 一律报错。
3. 校验目标：属于本 Run、`LoopPath == ""`、状态是 `waiting_user`、节点 kind 是 `agent`。
4. **幂等**：若本 Run 已存在 `PrevAttemptID == 目标.ID` 的行，直接返回它，不建第二次尝试。
   这样「apply 成功但游标没推进」的重放不会重跑模型。
5. 拒绝并发的在跑节点（同 `CreateWorkflowNodeAttempt` 的 open-attempt 语义）。
6. 建第 N+1 次尝试：`CreateAnsweredWorkflowNodeRun` —— 在 `CreateRetryWorkflowNodeRun`
   的基础上额外写入
   - `SessionKey = 原尝试.SessionKey`（`per_node` 策略下即续用同一段会话，agent 看得到
     自己上一次的对话）；
   - `Input["user_answer"]`、`Input["user_answer_activity_id"]`。
7. 账本：`WorkflowEventNodeRetried`，payload 带 `prev_attempt_id` / `answer_activity_id` /
   `answer_excerpt` / `reason: "user_answer"`。
8. 复用既有收尾：`resetWorkflowNodeProjection` → 任务置 `running` → 任务事件
   `workflow_node_answered` → `WakeWorkflowRun(..., "answer")`。

### 4. 答复真正被看到：prompt 注入

`prepareWorkflowNodeAt` 在 `resolveWorkflowNodePrompt` 之后追加一段答复块（只在
`NodeRun.Input["user_answer"]` 非空时）：

```
## 用户答复
你上一次在这个节点停下来向用户提了问题：
> <上一次尝试的 reason/summary/question/output 摘要>

用户现在答复：
<answer>

请基于这个答复继续完成本节点，不要重复已经做完的部分。
```

上一次的提问从 `PrevAttemptID` 指向的行读（`nodeRunText(prev, "reason", "question",
"summary", "error")`），取不到就省略引用段。

### 5. 用户看得见（不再静默）

`applyRouteAnswerWaitingNode` 把占位回复改写成一条**事实性回执**：

> 已把你的答复交给「<node name>」节点，正在带着答复重跑。

- 复用同步占位（用户已经看到「正在想」的那条），`status=completed`，`purpose=reply`，
  绑定 `reply_to_id` / `router_decision_id`，正常广播；
- 占位已被前一条动作占用时新起一条活动（与 `say` 的规则一致）；
- 回执不覆盖任务头：任务此时由服务置为 `running`，比 `waiting_user` 更准确。

失败时沿用 `failUnusedRoutePlaceholder`，占位显示失败原因（决策 `failed`，不回落旧路径）。

### 6. 装配

新增端口，风格与 `ActivityRunLifecycle` 一致：

```go
type ActivityNodeAnswerer interface {
    AnswerWaitingWorkflowNode(ctx context.Context, rootID, taskID, nodeRunID, body, activityID string) (NodeRun, error)
}
```

`Service.ActivityServiceForRoot` 里 `svc.SetNodeAnswerer(s)`；nil 时该动作明确报
「node answer port not configured」，而不是假装成功。

### 7. 校验

`validateRouterActions`：

- `answer_waiting_node` 计入 `executions`（与 run_workflow / message_agent 互斥），
  `body` 必填；
- `brief.ActiveExecution != nil` 且有活动节点时拒绝（应该先等这一轮结束）。

运行期事件白名单（`validateRuntimeEventActions`）不改：新动作天然被拒。

## 改动清单

| 文件 | 改动 |
|---|---|
| `kanban/router_types.go` | 新 kind、`RouterAction.NodeRunID`、`IsExecutionAction` |
| `kanban/router_tools.go` | `tools()` 注册新工具 |
| `kanban/router_tools_answer.go`（新） | 工具定义 + 目标解析闸门 |
| `kanban/router_tools_schema.go`（新） | 纯移动 `objectSchema` / `briefHasWorkflow` / `isRuntimeEventBrief`（给 500 行硬线留余量） |
| `kanban/router_prompt.md` | 工具清单 + 规则 2 的补充分支 |
| `kanban/workflow_node_answer.go`（新） | `AnswerWaitingWorkflowNode` + 内部收尾 |
| `kanban/task_store_workflow_runtime.go` | `CreateAnsweredWorkflowNodeRun` |
| `kanban/workflow_node_executor.go` | prompt 追加答复块 |
| `kanban/activity_route_apply.go` | `applyRouteAnswerWaitingNode` + 回执 |
| `kanban/activity_service.go` / `service_activities.go` | 端口 + 装配 |

无 HTTP 端点、无前端改动：回执走既有活动流，节点重跑走既有运行视图。

## 测试（RED → GREEN，逐条钉住行为）

| 测试 | 钉住什么 |
|---|---|
| `router_tools_answer_test.go::TestRouterAnswerWaitingNodeToolGate` | 无 waiting 节点 / HumanGate / 多 waiting 未指定 / 有 running 节点 / 正常记录（目标 NodeRunID 被钉进动作） |
| `workflow_node_answer_test.go::TestAnswerWaitingNodeRetriesWithAnswer` | attempt+1、`prev_attempt_id`、复用 session、`Input["user_answer"]`、原行不被改写、任务回 running、账本事件 |
| `workflow_node_answer_test.go::TestAnswerWaitingNodeIsIdempotent` | 重复调用返回同一行，不建第 3 次尝试 |
| `workflow_node_answer_test.go::TestAnswerWaitingNodeRejectsBadTargets` | 非 waiting_user / HumanGate / 空 id / 未知 id / 投影已推进 |
| `workflow_node_answer_test.go::TestAnswerPromptBlockOnlyAppearsForAnsweredAttempts` | 下一次节点的 prompt 含答复块与上一次提问；无答复时 prompt 逐字不变 |
| `activity_route_answer_test.go` | 动作走端口并改写占位为回执；端口缺失 / 目标已移动时决策 failed 且占位显示原因 |
| `router_tools_delivery_test.go`（既有，改断言） | 0 活动节点的 deliver 报错必须点名 `router_answer_waiting_node` |

变异验证：去掉 `Input["user_answer"]` 注入 → prompt 用例红；去掉幂等分支 → 幂等用例红；
去掉工具闸门 → 闸门用例红。

## 不做（明确边界）

- 不把答复扇出给多个 waiting 节点（宁可报错让模型问用户）；
- 不让普通答复替代 HumanGate 的批准/驳回（D4）；
- 不回答「运行中节点」的提问 —— 那是 `deliver` 的语义；
- 不改 `POST .../retry` 的既有契约（无消息重试仍是原语义）；
- 不覆盖 kill -9 / SQLite 语句中断下的跨进程幂等（与 R1 T9 的边界一致）。

## 执行记录（2026-11-19）

### 落地形态

| 环节 | 实现 |
|---|---|
| 动作 | `RouterActionAnswerWaiting = "answer_waiting_node"`，`RouterAction.NodeRunID`，计入 `IsExecutionAction` |
| 工具 | `router_answer_waiting_node(body, node_id?, reason?)`，当轮解析唯一目标并把 NodeRunID 钉进动作；运行期事件回合短路 |
| 闸门 | `resolveAnswerTarget`：有 running 节点 → 拒绝；HumanGate 停住 → 指向 approve/reject；0 个候选 → 拒绝；多候选未指定 → 列出候选；历史行（投影已推进）不算候选 |
| 服务 | `Service.AnswerWaitingWorkflowNode` → `CreateAnsweredWorkflowNodeRun`（attempt+1、复用 SessionKey、`Input["user_answer"]` / `user_answer_activity_id`、`PrevAttemptID`）→ `WorkflowEventNodeRetried`（`reason=user_answer`）→ `resetWorkflowNodeProjection` → 任务 running → 任务事件 `workflow_node_answered` → `WakeWorkflowRun(..., "answer")` |
| 幂等 | 先按 `PrevAttemptID` 找后继行并直接返回，再做任何目标校验（重放时原行已不在 waiting_user） |
| prompt | `withUserAnswerPrompt` 在 `resolveWorkflowNodePrompt` 之后追加「## 用户答复」块（上一次提问取 `question`/`reason`/`summary`/`error`，截断 600） |
| 回执 | `applyRouteAnswerWaitingNode` 复用占位并改写为「已把你的答复交给「X」节点，正在带着答复重跑。」；失败沿用现有失败路径显示原因 |
| 装配 | `ActivityNodeAnswerer` 端口 + `SetNodeAnswerer`，`ActivityServiceForRoot` 装配；nil 时报 `node answer port not configured` |
| 校验 | `validateRouterActions`：body 必填、与 run_workflow/message_agent 互斥、`ActiveExecution.CurrentNodes` 非空时拒绝 |

### 与设计稿的偏差（都是实现中发现的事实）

1. **「停没停」看 Run 投影，不看 NodeRun 行**：重试不改写原行，历史行永久停在
   `waiting_user`，只认行状态会把答复过的节点反复当成候选（RED：`TestAnswerWaitingNodeRejectsBadTargets`
   的 `not_waiting_user` 子用例在初版实现下反而成功）。工具与服务两侧都加了
   `run.NodeStates[node] == waiting_user` 守卫。
2. **幂等查找必须早于目标校验**：加上投影守卫后，重放会因「原节点已 success」失败，
   于是把 `PrevAttemptID` 后继查找提到最前（`TestAnswerWaitingNodeIsIdempotent` 钉住）。
3. **prompt 引用段的取值顺序**：`nodeRunText` 返回第一个非空键，初版顺序把 `reason`
   排在 `question` 前面，导致线上形态（reason 有值、problem 在 output）引用了错误文本；
   改为 `question` → `reason` → `summary` → `error`。

### 门禁

- `gofmt -l server/internal/kanban` → 空。
- `go test -count=1 ./...` → 28 包全绿（首轮 kanban 出现一次既有 flaky 失败
  （sqlite I/O），单包复跑与全量复跑均绿）。
- 定向新用例：`TestRouterAnswerWaitingNodeToolGate`、`TestAnswerWaitingNodeRetriesWithAnswer`、
  `TestAnswerWaitingNodeIsIdempotent`、`TestAnswerWaitingNodeRejectsBadTargets`、
  `TestAnswerPromptBlockOnlyAppearsForAnsweredAttempts`、
  `TestApplyRouteAnswerWaitingNodeRendersConfirmation`、`TestApplyRouteAnswerWaitingNodeFailureIsVisible`。
- 变异验证 4 处（各自打红对应用例后还原）：去掉 `Input["user_answer"]` 注入 → prompt 用例红；
  去掉幂等分支 → 幂等用例红；去掉工具闸门的目标解析 → 闸门用例红；去掉投影守卫 → 目标校验用例红。

### 未做 / 残留

- 未在隔离实例里用真实模型跑「停住 → 答复 → 节点重跑」全链路；本条改动只走既有
  节点生命周期，但真实模型的答复质量未验收。
- 未覆盖 kill -9 / SQLite 语句中断下的跨进程幂等（与 R1 T9 边界一致）。
- 未 push、未部署；本机 system 单元重启仍需用户执行。
- `make check-upstream`（在干净 worktree 的提交树上跑 `go run ./scripts/check-upstream`）：
  G-MT 组自身全绿（23 个锚点全部命中、4 个测试文件都可被 `go test ./...` 纳管）。
  全仓仍有 4 项红：`server/internal/api/{http_,usecase/}session_worktree_ownership*` 未登记 ——
  它们来自上一个提交 `702e6ef1`（G-MQ 会话工作树归属），登记块还停在共享工作区未提交
  （另一条会话在飞）；本提交前后该红逐条一致，未引入新缺口。G-MJ 的一个锚点随
  `isRuntimeEventBrief` 迁到 `router_tools_schema.go`，同提交更新。
- 提交树上的 `gofmt -l` 为空，`go test -count=1 ./...` 28 包全绿（`07a5d2db`）。
