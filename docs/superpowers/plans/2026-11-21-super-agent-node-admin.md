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

**W1、W2 已完成，W3 进行中。** W1 组号 **G-PB**（计划首次提交时记作 G-NY，与同日的移动端任务
详情面板组撞号，以本文件与 `docs/upstream-customizations.yaml` 的 G-PB 为准）；W2 组号
**G-PD**。

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

### T2 完成（2026-11-21）

- 配置：`router.supervision.max_actions_per_run`（默认 12）与 `max_actions_per_hour`
  （默认从 6 改为 20，用户裁定），只走 `config.json`；`cli/cmd` 与 `server/cmd/mindfs-server`
  两条启动路径都透传，`app.routerConfigFromOptions` 落到 `kanban.RouterSupervisionConfig`。
- 计数口径：一个决策里每个**有副作用**的监管动作计 1（`retry_node` / `resume_run` /
  `retry_direct` / `notify_user` / `escalate` / 三档 cancel），`say` / `ask_user` / `silent`
  不计。只数 `applied` / `applying` 的决策 —— 被校验拒绝的回合没有副作用。
- 两个窗口独立：本 Run（`trigger_ref` 认裸 `runID` 与 `<runID>:a<N>` 两种实例键）与同任务
  滚动一小时；`SupervisionObservation` 新增 `actions_this_run` / `run_action_budget`。
  直连回合没有 Run，只受小时额度约束。
- 用尽：当前动作不执行，`supervisionBudgetError` → 事件 reason
  `supervision_budget_exhausted`（写明是哪个窗口）+ 可见系统说明 + 任务 `waiting_user`；
  **绝不自动取消**，也不允许用取消绕过预算。
- 验证：`router_supervision_budget_test.go` 4 条（默认值 12/20、Run 额度用尽停
  `waiting_user` 且节点仍 `running`、小时额度按动作而非回合计数、say/ask 不计且有正对照），
  `cli/cmd` 与 `server/app` 配置透传用例按新字段更新。四处变异各自打红后还原：按决策回合
  计数、去掉 Run 窗口、把 say 也计入、预算用尽改成终态任务。

### T3 完成（2026-11-21）

- 新表 `task_acceptance_items(id, task_id, run_id, node_id, seq, text, evidence_class, status,
  evidence_ref, rework_round, created_at, updated_at)` + `idx_acceptance_task_run`，删任务时
  级联清理。ID 确定（`acc-<task+run 哈希6>-<seq>`），同一轮重复登记按 `(task, run)` **整体
  替换**，不追加。
- Router 新工具 `router_record_acceptance(items)`：1–8 条，每条 `text` + `evidence_class`
  （`self_report` / `artifact` / `independent_verification`）+ 可选 `evidence_ref` /
  `node_id`；归一化时拒绝空文本、未知等级、`artifact` 缺路径。动作在 `run_settled` 与
  `completion_blocked` 两类运行期事件回合里同样允许（与 `complete_task` 同轮）。
- `record_acceptance` 的应用路径只写验收项、不动任务状态；没带 `run_id` 时绑任务当前这一轮
  （直连任务保持空 run 命名空间）。
- 证据门扩展：`EvaluateCompletion` 在契约核对之后附加验收项并**重算整体 `Passed`**。
  `self_report` 永远不过（Detail 说明「只是自述」，逼出更好的证据）；`artifact` 复用
  `file_exists` 的工作区根取值、相对路径校验与符号链接守卫（含父目录是链接的逃逸），
  `Lstat` 再拒最后一段是链接的情况；`independent_verification` 让
  `CompletionReport.AcceptanceNeedsVerification` 置位，`completionNeedsVerifier` 在**有契约**
  时也照常起独立复核，结论 pass 时 `markAcceptanceVerified` 把项改成通过再 `commitCompletion`
  （落库报告同步是 passed，不留自相矛盾的记录）；`commitCompletion` 顺手把项标 `met`。
- 返工轮数复用既有补做预算（默认 2 轮，第 3 次停 `waiting_user` 并列缺口编号），
  `rework_round` 只是审计水位；`blockCompletion` 的 bump 放在「同一决策重放」早退之后，
  重放不会重复计数。
- 提示词：工具清单与规则 16/19 写明三类证据、整体替换、`self_report` 不算证据、补做回合
  按等级处理缺口；运行期事件回合的白名单一行补上 `router_record_acceptance`。
- 验证：`acceptance_items_test.go` 10 条（自述只是声称、产物必须真实存在、链接逃逸两例、
  独立复核等待与 pass 收尾、动作校验矩阵、工具记录、运行期白名单、存储替换与返工水位、
  复核 pass 收尾并标 met、驳回按决策计一轮返工），六处变异各自打红后还原（有契约就不问
  复核 / pass 不标记项 / 跳过符号链接守卫 / 自述算通过 / 不重算整体 Passed / 重放也记返工）。
  `go test ./server/internal/kanban/` 全绿（274s，首跑一条既有 sqlite 并发 flake，复跑全绿）。

### T4 完成（2026-11-21）

三个窗口的落地，其中两个在写测试时直接暴露了 W1 T1 的真实缺陷 —— 不是「崩溃才出现」，
而是**正常在线也会发生**：

- **节点级取消后整轮停摆**（根因，在线路径）：`CancelWorkflowNodeForRouter` 只把行改成
  `cancelled`，从没写 run 投影；driver 醒来按投影重算时这个节点仍是「没跑过」，于是它
  反复派发一个已取消的行，实测空转到 `workflowAdvanceStepLimit`（1000 步）报错，下游
  节点一次都没被派发。修复两处：新增 `TaskStore.CancelWorkflowNodeRunProjected`，在同一
  事务里「行 → cancelled + `node_cancelled` 事件 → run 投影 + revision CAS」；`EngineReadyNodes`
  把「前驱已被取消」按「已满足」处理（原先只有 success / skipped 放行，取消会让所有下游
  永远 ready 不了）。Loop 内部行沿用既有规则不写 run 级投影。
- **`cancel_run` 写到一半崩**：`run_cancelled` 事件是幂等依据。启动对账发现该事件存在时，
  按账本把还没收尾的投影节点冻结成 `cancelled` 并补一条 `reason=startup_cancel_replay` 的
  事件、结算仍 running 的回复活动、任务从 `running` 放回 `waiting_user`；第二次启动
  `workflowRunHasOpenNodes` 为 false，不写任何事件。
- **只取消了节点、driver 没被唤醒**：没有 `run_cancelled` 账本的一轮不按整轮取消处理 ——
  先把「行已 cancelled、投影没写」的节点补进投影（每节点只看最新一次尝试，重试成功的
  不会被旧取消覆盖），任务仍在 `running` 时才唤醒 driver 继续跑下游；`waiting_user` /
  暂停的任务不替操作员继续。
- **预算不从内存重建**：`supervisionActionUsage` 本来就是按 `router_decisions` 现算，本轮
  补一条用例把它钉住（新 `Service` 读到的用量必须与重启前一致），防止以后有人加进程内
  缓存。
- 验证：`workflow_recovery_router_cancel_test.go` 4 条（整轮取消重放 + 幂等、只取消节点
  的启动唤醒、在线 `CancelWorkflowNodeForRouter` 写投影并推进下游、预算重启口径），六处
  变异各自打红后还原（忽略 `run_cancelled` 账本 / 不把任务放回 `waiting_user` / 去掉两处
  幂等守卫 / 不补投影 / 引擎仍把取消当前驱阻塞 / 取消只写行不写投影）。
  `go test ./server/internal/kanban/` 全绿（314.8s）。
- 边界：不宣称跨进程副作用恢复（沿用 G-KV）；取消发生在 agent 回合中途时，中断仍走
  `interruptWorkflowNodeRuns` 的既有语义。

---

## W2 执行记录

### T1 完成（2026-11-21）

- `RouterBriefRun` 新增 `nodes`（逐节点卡片）与 `node_cards_truncated`，`routerBriefVersion`
  1 → 2。每张卡 `node_id` / `kind` / `name` / `status` / `attempt` / `summary`（≤240 字节）/
  `actions`，按图顺序排列；状态取投影、行做补充，所以「行已 running、投影还停在 pending」
  的窗口里卡片显示的是 running（投影落后是常态，不能让 Router 以为节点没动）。
- 动作表 `briefNodeActions` 是唯一口径：queued/running → 只有 `cancel_node`；
  `waiting_user` 的 agent 节点 → `answer_waiting_node` + `cancel_node`，human_gate → 无动作；
  fail/unknown → `retry_node` + `set_node_model`；从未跑过的 agent 节点 → `skip_node` +
  `set_node_model`；success/skipped/cancelled → 无动作。「运行中不切换」因此落在数据层，
  不依赖提示词自觉。
- 卡片与 `active_execution.current_nodes` 合并成同一个 `RouterBriefNode` 类型（后者原来
  只有 `node_id` / `name` / `status` / `session_present`），避免「节点长什么样」出现两套结构；
  `current_nodes` 的 JSON 字段名未变。
- 上限与裁剪：单次 brief 最多 16 张卡，超出只报总数；`capSituationBrief` 逼近 24 KiB 时
  先缩摘要到 120 字节，再缩到前 4 张并把省略数累加进 `node_cards_truncated`。
- 验证：`router_brief_nodes_test.go` 4 条（卡片状态/摘要/动作、运行中只有取消、16 张上限、
  brief 超限时卡片先缩摘要）。五处变异各自打红后还原（卡片不看行状态 / 运行中也给改档 /
  去掉上限 / 失败卡不给改档 / 裁剪不缩摘要）。`go test ./server/internal/kanban/` 全绿（230.5s）。
- 边界：本任务只做读侧；`inspect_node` / `set_node_model` / `skip_node` 的动作与校验在 T2，
  卡片里先给出动作名，T2 落地后才会被 `validateRouterActions` 接受。

### T2 完成（2026-11-21）

- **两个写动作 + 一个读侧细化**，全部走既有「工具只记录、apply 统一执行」的通道：
  - `router_set_node_model(node_id, model, reason?)`：把 `params.model` 写进本轮 Run 的图
    （`setGraphNodeParam` 写时复制），下一次 attempt 生效；改档本身不触发重跑。
  - `router_skip_node(node_id, reason?)`：复用驱动既有的 `commitSkippedWorkflowNode`
    （补一个 `reason` 参数写进 `output.reason`），写完 `WakeWorkflowRun` 让下游继续。
  - `router_inspect_run` 加可选 `node_id`：只回该节点的 `{node_id, attempt, status, summary
    (≤4000), output, session_key}`，不再重复整轮节点清单；从未跑过的节点按图状态返回
    pending，不在图里则报错。**不新增第 19 个工具** —— 工具定义本身要吃 token（上一轮刚把
    36 个内置工具关掉省了 ~49k/回合）。
- **「运行中不切换」三层各自拦一次**（不是重复，而是三份不同的证据）：
  - 工具层 `requireNodeAction` 按当轮卡片的 `actions` 判，拒绝时说的是人话（「节点 X 正在
    运行（running），运行中不切换；先 router_cancel_node，或等这一回合结束」）。
  - `validateRouterActions` 拿**同一份 brief 快照**再判一次 —— 动作收集完到 apply 之间
    模型可能又调了别的工具，卡片口径不变但动作集合可能已经越界。
  - 服务层 `SetWorkflowNodeModelForRouter` / `SkipWorkflowNodeForRouter` 按**行**判
    （`nodeRunInFlight` 看最新一次 attempt 是 queued/running），补上「卡片生成后节点刚好
    被驱动派发出去」的窗口。
- **跳过的语义边界**：只接受**从未有过 attempt** 的节点（`nodeRunStarted` 为假）。失败过的
  节点跳过不会让下游动（下游停的是它的失败而不是它的缺失），所以直接拒绝并指路「用重试或
  改档」；投影里已是非 pending 状态的同样拒绝。改档则相反 —— 失败 / 已收尾节点都允许改，
  因为改档不改变这一轮已发生的事实。
- **动作落地**：新增端口 `ActivityNodeAdmin`（与 `ActivityNodeRetrier` / `ActivityRunCanceller`
  同形，装配在 `service_activities.go`），apply 后各落一条确定性系统行
  （`route-model:<decision>:<seq>` / `route-skip:<decision>:<seq>`，`ActivityTypeAttention`），
  用户在任务详情里看得见「谁把哪个节点改成了什么」。
- **提示词**：`router_prompt.md` 增规则 20（先看卡片 `actions`、运行中不切换、反复失败先改档
  再重试、跳过前确认下游不依赖其产物、两个动作都要写 reason），并在「你能看到的」里说明
  `latest_run.nodes` 的字段口径、在工具清单里补两个新工具与 `inspect_run(node_id=...)`。
- 验证：`router_node_admin_test.go` 9 条（工具记录与校验、运行中拒绝、改档写进图且 revision
  前进、服务层运行中拒绝、跳过解锁下游（scripted runner 真跑一轮、`runCalls==1`、下游投影
  success）、跳过拒绝运行中、跳过拒绝已跑过、校验层按卡片拒绝、`inspect_run` 只回单节点、
  apply 走端口并留系统行 + 端口未装配时明确报错）。五处变异各自打红后还原（工具层不看卡片
  动作 / 服务层去掉 in-flight 守卫 / 跳过不唤醒 driver / `inspect_run` 忽略 node_id /
  校验层跳过节点动作检查）。`go build ./...` 与 `go test ./server/internal/kanban/` 全绿
  （279.8s）。
- 边界：节点级动作只认**顶层 agent 节点**（Loop 内部节点、human_gate 拒绝）；`set_node_model`
  不校验模型档是否存在于当前 harness 的可用列表（与既有按任务选模型同口径，配错时由 agent
  层报错并落 unknown）。

### T3 完成（2026-11-21）— W2 收尾

- **卡片带模型档**：`RouterBriefNode.model`（`params.model`，空 = 跟随 harness 默认，
  `graphNodeModel`）。改档前先看见现值，才谈得上「判」—— 不然 Router 只能盲改，用户问
  「为什么这个节点慢」时也答不上来。同时提示词规则 20 要求「别改成一样的」。
- **「改 workflow」与改档同一条规矩**（用户裁定「运行中不要切换」）：
  `EditWorkflowRunGraph` 早已按「已触发节点」冻结图编辑，本轮把它与 W2 打通并留回归钉子 ——
  `TestEditWorkflowRunGraphRejectsRunningNodeModel` 用**只有行、投影还停在 pending** 的
  运行中节点验证：改 `params.model` 被 `ErrFrozenNodeEdit` 拒绝；同一张图里**还没跑过**的
  节点仍可改（不过度封锁）。
- 交叉轮切换（已结算的 Run 之后换一条 workflow 重开）在 `router_brief_active_execution_test.go`
  已有契约，本轮不重复造。
- 验证：`TestBriefNodeCardCarriesModel` 先红（字段不存在）后绿；两处变异各自打红后还原
  （卡片不带 model / `workflowRunFrozenNodes` 不看行只认投影）。`go build ./...` 与
  `go test ./server/internal/kanban/` 全绿（264.3s）。
- W2 边界（不再扩）：节点级动作只认顶层 agent 节点；模型档不校验 harness 可用列表；
  跨轮历史卡片不做。

## W3 — 自设计 workflow（进行中）

用户裁定：「Router 应该要会自己设计生产或者派生总结需要的 workflow」，「发新的 / 改已有的
workflow 需要显式批准，项目级别、临时使用的可以自由生成，但固化、提升到全局的 workflow
需要批准」。落成三档，本轮先做第一档。

### T1 完成（2026-11-21）— 临时图直接跑（项目内免批）

- **动作**：`router_run_workflow` 新增 `graph` 参数；与 `workflow_id` / `version_id`
  **互斥**（工具层 + 校验层各拒一次 —— 决策落库后可能被重放，校验层不能只信工具层）。
- **起 Run**：`resolveRouteWorkflowVersion` 合成只属于这次 Run 的
  `WorkflowVersion{WorkflowID:"adhoc", Version:"adhoc:<sha256[:8]>", Graph:...}`，
  走既有 `beginWorkflowRunVersion`。控制面目录一个字都不写，Run 自己快照整张图 ——
  所以不存在「目录里出现一张没人批准过的 workflow」。
- **作用域**：`sanitizeRouterGraph` 一律清空 `RootID`，apply 阶段填 `task.RootID`。
  图里 `root_id` 为空在 MindFS 语义里等于**全局**，那正是需要显式批准的一档。
- **闸门**：节点 ≤ 12、边（含 Loop 子图）≤ 24、整图 JSON ≤ 64 KiB；结构走
  `ValidateWorkflowDefinitionForAuthoring`（悬空边、环、未知 kind、末端 gate 都拒）。
- **提示词**：规则 21 写清形状、上限与「固化 / 覆盖目录版本现在没有这个权限」。
- 验证：3 条用例先红（`Graph` 字段不存在）后绿 —— 端到端起 Run（含 `RootID` 落在任务
  项目上、目录版本数不变）、来源互斥、12/13 节点边界与悬空边、校验层复查落库动作；
  5 处变异各自打红后还原（工具不记录图 / 两层互斥都去掉 / 节点上限放开 / 不填作用域 /
  校验层不复查）。`go test -run 'Router|Workflow|Brief' ./server/internal/kanban/` 全绿。
- 边界：T1 只放开「这一轮临时用」；固化、派生、模型档层级见 T2–T4（未实现前 Router 没有
  写控制面的工具）。
