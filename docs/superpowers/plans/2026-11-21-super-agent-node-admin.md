# Super agent 升级：节点级管理员 + 自设计工作流

Plan 2026-11-21。上一轮（G-NQ 主动监管 S0–S4）之后，用户裁定 super agent 的下一步：
把「主动监管、验收返工、故障恢复」做完整，再把 Router 提升到节点级管理员，最后让它
自己设计/派生工作流。本文是这四波的可执行计划；按波推进，每波独立提交、独立门禁。

## 用户裁定（规划输入，不再重问）

1. **解禁 cancel**：监管动作白名单放开取消（节点 / Run / 任务三档）。
2. **workflow 三级审批**：新发 / 改已有 workflow 需要显式批准；**项目级、临时使用**的
   可以自由生成；只有**固化、提升到全局**的 workflow 需要批准。
3. **节点 / workflow 间可以切换**，但**运行中不切换**（只在回合之间换）。
4. 机器管理（节点上下线、部署、重启）放到 **R4**，本轮不做。
5. 其余六项按建议默认值执行（见下）。

## 已定的默认值

| 决策项 | 取值 |
|---|---|
| cancel 范围 | `cancel_node` / `cancel_run` / `cancel_task` 三档；**不自动取消** HumanGate 等待中的节点；worktree 一律保留；优先最小档（能取消节点不取消 Run） |
| 预算 | 每 Run 12 个监管动作、每小时 20 个；用尽 → `notify_user` + 任务停 `waiting_user`，**不自动 cancel** |
| 验收项 | 独立表存储；证据分三档（自述 / 产物 / 独立复核）；最多 2 轮返工 |
| 模型切换 | 节点级覆盖（`params.model`），只在回合之间生效 |
| 审批位置 | 任务详情「待批准」卡（Router 提案 / workflow 固化）；不阻塞其它任务 |
| 工作流审批 | 草稿与项目内临时 workflow 免批；提升为全局 / 覆盖既有版本必须批准 |

## 波次

| 波 | 内容 | 依赖 |
|---|---|---|
| **W1 底座** | cancel 三档、预算、验收项与证据等级、故障恢复补口 | 无 |
| **W2 节点级管理员** | Router 直接管理节点：查看节点、逐节点重试 / 取消 / 改档、运行中不切换 | W1 |
| **W3 自设计工作流** | Router 起草 / 派生 workflow，三级审批，节点级模型档 | W1、W2 |
| **R4 机器管理** | 节点上下线、部署、重启 | 另立计划 |

---

## W1 底座

### T1 cancel 三档（`cancel_node` / `cancel_run` / `cancel_task`）

- 新增动作与工具：`router_cancel_node(node_id, reason)`、`router_cancel_run(reason)`、
  `router_cancel_task(reason)`。三者都不与其它执行动作同轮（取消是收口，不是开工）。
- 可用性：`cancel_node` 只认当前 open 的 Agent 节点（HumanGate 等待中的节点拒绝 ——
  用户裁定「不自动取消等待审批的节点」）；`cancel_run` 要求有活动 Run；`cancel_task`
  拒绝终态任务。任务级取消只在「Run 已经停了、但任务还挂着执行态」时使用，
  避免用 cancel 绕过 `complete_task` 的证据门。
- 落地实现：节点级走 `TaskStore.CancelWorkflowNodeRun`，Run 级走
  `Service.CancelWorkflowRun`，任务级走 `Service.Cancel`；取消后唤醒 driver 让下游
  按 skip 语义收敛，并把取消原因写进任务可见事件（`node_cancelled` / 系统行）。
- 取消不删 worktree、不删会话；被取消节点保留尝试记录与输出。
- 所有取消计入预算（T2），且**预算用尽只通知不取消**。

**RED 用例**：① 节点级取消 → 该节点 `cancelled`、下游 skip、Run 收敛、任务回
`waiting_user`；② gate 等待中的节点拒绝 `cancel_node`（错误码可读）；③ 终态任务拒绝
`cancel_task`；④ 取消与重试同轮 → 整条决策 rejected；⑤ 取消后 worktree 与会话仍在。

**变异**：去掉 gate 等待保护 → ② 红；去掉终态保护 → ③ 红；允许同轮组合 → ④ 红。

### T2 预算（每 Run 12 / 每小时 20）

- `RouterSupervisionConfig` 增加 `MaxActionsPerRun`（默认 12），沿用
  `max_actions_per_hour`（默认 20）。只走 `config.json`。
- 计数口径：一个决策里每个「有副作用的监管动作」计 1（`retry_node`、`resume_run`、
  `retry_direct`、`notify_user`、`escalate`、三档 cancel）；`say` / `ask_user` 不计。
- 用尽：当前动作不执行，改落一条确定性的 `notify_user` 系统行（写明用尽的是哪个
  额度、本 Run 已用多少），任务转 `waiting_user`，事件 reason
  `supervision_budget_exhausted`。**绝不自动 cancel。**
- 预算按 `(task_id, run_id)` 与滚动 1 小时两个窗口独立判，任一用尽即停。

**RED 用例**：① 第 13 个 Run 级动作被拒 + 通知行出现 + 任务 `waiting_user`；② 小时
额度用尽覆盖跨 Run 场景；③ `say` 不计入；④ 用尽后不再产生任何取消动作（变异面）。

### T3 验收项与证据等级

- 新表 `task_acceptance_items(id, task_id, run_id, node_id, seq, text, evidence_class,
  status, evidence_ref, rework_round, created_at, updated_at)`；`evidence_class` ∈
  `self_report` / `artifact` / `independent_verification`。
- 来源：`complete_task` 的 summary 之外，Router 可用新工具
  `router_record_acceptance` 登记本轮验收项（运营口径：一轮 Run 结算时至少要有一条）。
- 证据门（G-NP）改为逐项核对：`self_report` 只算「声称」，`artifact` 要求指向真实
  文件/命令产物，`independent_verification` 要求独立复核回合的结论；未达标的项留在
  `open`，最多自动返工 2 轮，第 3 次转 `waiting_user` 并列出缺口编号。
- 全部达标才允许 `complete_task` 把任务推进到 `success`。

**RED 用例**：① 三条验收项证据分档正确落库；② 只有 self_report 的项不能收尾；
③ 返工 2 轮后第 3 次停 `waiting_user` 且缺口可见；④ 证据达标后收尾成功。

### T4 故障恢复补口

- 覆盖三个已知窗口：进程在「节点已取消、driver 未唤醒」间死掉；`cancel_run` 后
  重启；预算用尽后重启不得重复扣预算。
- 启动对账扩展为：取消类事件幂等重放（同一 `node_cancelled` 不重复写）、预算计数从
  持久化事件重算（不信任内存）。
- 不宣称跨进程副作用恢复（沿用 G-KV 边界）。

---

## W2 节点级管理员（W1 之后）

- Router brief 增加「逐节点卡片」：状态、attempt、最后一次输出摘要、可用动作。
- 新动作 `set_node_model`（节点级模型档）、`skip_node`（按 D6 语义跳过）、
  `inspect_node`（读节点输出，不读全量转录）。
- 「运行中不切换」：节点 `running` 时拒绝改模型 / 改 workflow；只在回合之间生效。
- 与 W1 的 cancel 合成「节点级管理员」闭环：看 → 判 → 重试 / 取消 / 改档 → 唤醒。

## W3 自设计工作流 + 三级审批（W2 之后）

- Router 起草 workflow（项目内临时，免批）→ 直接跑；提升为全局或覆盖既有版本 →
  落「待批准」卡，人在任务详情批准后生效。
- 派生：从既有 workflow 复制并改（记录 `derived_from`），同样按目标层级判审批。
- 模型档并入 workflow：节点 `params.model` 优先于任务级，任务级优先于全局。

## R4 机器管理

节点上下线、部署、重启、版本核对。本轮只记录，不设计。

---

## 执行纪律

- 每任务 RED→GREEN→变异验证；变异至少一处打红后还原。
- 每波结束跑 `go test -count=1 ./...`、`tsc -b --noEmit`、`npx vitest run`，并在干净
  worktree 跑 `make check-upstream`。
- 提交标 `Scope: G-X` 并登记 `docs/upstream-customizations.yaml`；一条计划一个组号。
- **不部署、不重启任何节点**，直到该波门禁全绿且用户明确要求 ship。

## 状态

**W1 进行中。** 组号 **G-PB**（计划首次提交时记作 G-NY，与同日的移动端任务详情面板组撞号，
以本文件与 `docs/upstream-customizations.yaml` 的 G-PB 为准）。

### T1 完成（2026-11-21）

- 动作与工具：`router_cancel_node(node_id, reason)` / `router_cancel_run(reason)` /
  `router_cancel_task(reason)`；进 `supervisionIsWriteAction`、运行期事件白名单、
  `validateRouterActions` 与 `validateSupervisionActions`。
- 校验：`cancel_node` 必须命中本轮 open 节点；`NodeKindHumanGate` 或 `NodeRunWaitingUser`
  一律拒绝（用户裁定「不自动取消等待审批的节点」）；`cancel_run` 要求还有 open 节点；
  `cancel_task` 要求 reason。一轮最多一个取消，且不能与重试 / 唤醒 / 回复 / 开工同轮，
  只可搭 `say` / `notify_user`。
- 落地：`workflow_runtime_router_cancel.go` —— 节点级 `CancelWorkflowNodeRun` + 唤醒 driver；
  Run 级先取消 open 行、停 driver、再扫一遍、写投影、任务落 `waiting_user` 并结算回复；
  任务级先显式拒绝终态任务（`Service.Cancel` 本身对终态幂等，静默成功会误导 Router），
  再走人工取消同一条路径。三者都不删 worktree / 会话 / 尝试记录。
- 可见性：`node_cancelled_by_router` 任务事件；系统行 `route-cancel:<decisionID>:<seq>`；
  前端监管记录认这三档动作并给了 zh/en 标签。
- 验证：`router_supervision_cancel_test.go` 6 条（节点级取消、Run 级停轮且任务回
  `waiting_user`、gate 等待拒绝、组合拒绝、终态任务拒绝、取消保留节点行与会话绑定）。
  四处变异各自打红后还原：gate 保护、组合规则、Run 级落 `cancelled` 而非 `waiting_user`、
  终态任务保护。`go build ./server/...`、kanban 定向全绿；全量 kanban 唯一红项
  `TestCoordinatorTickReclaimsStaleClaims` 是既有 sqlite 并发 flake，单跑通过。
- 提示词：新增三档工具说明；删掉「禁止 cancel Run」的总禁令，改为「只能走监管取消工具，
  最小够用、收口动作不与开工同轮、不许绕预算」。
