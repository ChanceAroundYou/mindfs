# Kanban-First Agent Workflows — 分层执行路线图 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans task-by-task. This roadmap coordinates child implementation plans; it does not authorize implementation by itself.

**Goal:** 将已建立的留言板/WorkflowRun 模型补成可验证、可恢复、具备安全 lane isolation 的单节点工作流运行体系。

**Architecture:** 先复现并确认主/子会话事件归属，再按 TDD 实现 durable WorkflowRun driver/Runner 接口、节点/评论/outbox 绑定、worktree 生命周期，最后开放 Agent-first typed patch/proposal。每个子系统独立验收，但共用的 NodeRun、outbox、TaskStore 与 Runner 接口须明确单一 owner 并串行集成。

**Tech Stack:** Go module `go 1.25.0`（项目工具链 1.26.5）；React/TypeScript；SQLite task-node store；现有 Agent Session/Pool 与 gitview。

**Spec:** `docs/superpowers/specs/2026-11-09-kanban-first-agent-workflows-design.md`；既有阶段状态：[WP-01…WP-07](2026-11-09-kanban-agent-workflows.md)。

## Global Constraints

- 仅单节点恢复；不承诺跨节点接管或 exactly-once 外部副作用。
- WorkflowVersion/Draft 在控制面；WorkflowRun/NodeRun/TaskActivity/ContinueRequest 在任务所属节点数据面。
- 留言绑定提交时活动 Agent NodeRun；steer 只有真实协议回执才按回执记录；否则该 NodeRun 结束后 FIFO 投递到同一 session。
- Agent Silent/Blocked 不算 success；Unknown 不自动重试；HumanGate 需明确批准；外部副作用不扩权。
- 执行代码须用 TDD；fake Agent/session/worktree adapters；不依赖真实 CLI 或生产数据。
- 不重启本机 `mindfs.service`，不部署/推送/合并；每个子计划实现完成后需独立获得实施/ship 授权。

## Review Focus

- 子代理 parent/child event owner 假设必须可证伪，且不改变合法 parent task-summary 卡行为。
- 两种 NodeRun/outbox 数据模型及两条 comment writer 路径必须收敛到可幂等映射。
- 每账户 Service 下共享物理 worktree 的写互斥必须 fail closed；不得把当前未接线的 gate 称作 durable gate。
- `EvalSafeExpression` grammar / 前端镜像 validator 在路径语法变更时同步升级。
- no-auth 仓库规则下，Agent-first 共享 Draft 写权限与人类确认身份必须明确；不可把 author 字符串当凭证。

---

## 目标与完成定义

把当前已落地的 WorkflowRun 数据模型与状态算法，补成可安全使用的单节点工作流：任务可被可靠地启动/推进；Agent 节点绑定稳定的 NodeRun/session；留言按已确认语义投递到提交时的活动 Agent NodeRun；Condition、HumanGate、bounded Loop 与状态投影可重启恢复；节点 workspace 声明接入 worktree 生命周期；Agent 只能在明确授权边界内提出或修改草稿。

完成不等于“状态机函数和 API 存在”。每个纵向阶段必须由失败测试证明触发、落库、失败/恢复和 UI/状态投影行为；真实 agent CLI 不作为单测依赖。阶段末统一跑 Go 全量测试、前端测试/类型检查、`make build-web`、`make check-upstream`；已知基线失败需重新核验，不能永久豁免。

## 当前事实（只读核验）

- `workflow_engine.go` 提供 `EngineReadyNodes`、`EngineEvaluateLoop`、`ProjectStatusFromRun` 等状态算法，没有生产驱动者。`executeTask` 对 WorkflowRun 返回 `ErrTaskAlreadyWorkflow`，显式迁移后的任务目前不会推进。
- `workflow_runs` / `workflow_node_runs` 已在任务节点 SQLite；`UpsertWorkflowNodeRun` 有持久化实现，运行视图有读取 API，但无生产执行器调用方。
- 既有 Kanban `Runner` 还提供 `CreateTaskWorktree` 和 `TaskUpdated`，但没有 workflow-node 执行接口。执行器须加专用端口；不通过合成 StageRun 双写 legacy 表。
- WP-03 的 `node_runs`、`continue_requests`、`outbox_items` 与 WP-05 的 `workflow_node_runs` 语义不完全相同；当前 `PostComment` 与 outbox/runtime NodeRun 未组成生产闭环。运行时计划必须锁定 `TaskStore.PostComment` / `OutboxService.RecordComment` 写入职责、legacy row 映射与唯一权威，不能默默双写。
- `workflow_runs.task_id` 当前唯一；若允许一个 Task 多个历史 WorkflowRun，SQLite schema rebuild、`WorkflowRunForTask` 与 `TaskEngine` 绑定读写须同步调整。
- `EnsureNodeWorktree`、`AutoMergeWorktree`、`CloseNodeWorktree` 和 `WriterGate` 都没有生产调用方；现有 Gate 按 `(task_id,worktree_id)` 且仅进程内，Kanban Service 按账户构造。跨账户共享物理 worktree 的 gate 尚未解决。
- `workflow_node_runs` 尚无 `worktree_id/path`；`task_worktrees` 同 `(task_id,worktree_id)` 冲突当前会更新 owner；branch identity 需覆盖。
- `EvalSafeExpression` grammar 在 `workflow_validate.go` 定义，现有 fixture 使用 `result.score`；改用 `node_id.output.*` 必须同步后端 evaluator 与前端 `workflowModel.ts` / tests。
- Agent config 属机器数据面；所属节点缺少 Run snapshot 指定的 Agent/model 时不能静默回退。
- REST/WS 不做鉴权、`user=` 不是安全边界；WorkflowStore 是共享控制面。Agent-first 不得暗中新增冲突的 REST auth。

### WP-00 前置缺陷候选：主/子会话事件隔离

用户报告“子会话一直往主会话塞消息”。这是症状，根因未证实。在线 router 有故意保留的 parent Task lifecycle/summary 卡；child 内部卡必须与 parent 可见卡分开。候选路径包括 router fallthrough、后台 child subscription、import/reopen binding、CLI task-notification。先确认用户会话实际路径并写最小 fake-stream reproducer；不得静态猜根因或触碰生产数据。

WP-00 的 RED 不得要求删除现有测试锁定的合法 parent Task/summary cards，也不得把已由 router 消费并刻意 swallow 的 late event 强行改成落盘。若不能重现 child-owned 内容污染 parent，写清诊断限制并保持 Executor gate blocked，不虚报修复。

## 已确认的留言语义

1. WorkflowRun 中新留言绑定提交时的当前活动 Agent NodeRun，并固定其 `node_run_id` / `session_key`；后续调度不得漂移归属。
2. 只有 Agent NodeRun adapter 声明 steer 且返回真实协议回执时，按回执记录状态；`sent` 不等于 `confirmed`。不支持/拒绝时进入 FIFO，待绑定 NodeRun 结束后将留言投递到同一 session；每条留言独立 ID/状态。
3. 同一 session 不并发第二个 turn；不确定外部发送窗口标 unknown，不自动重放。
4. 原 spec 的 Version 不可变、Draft/Run CAS、Loop 1–20 默认 5 且不嵌套、unselected branch skip/join、unknown 不自动重试、merge conflict needs-user、legacy/workflow 引擎互斥、单节点恢复均有效。
5. Agent 发布共享 WorkflowVersion 必须由用户确认具体 revision；此项已由原 spec 决定，不再列作开放问题。

## 未决产品决策（在对应 implementation RED 前确认）

推荐默认不是决定。

| ID | 未决行为 | 建议默认 | 阻塞/影响 |
|---|---|---|---|
| D1 | 谁启动/驱动 WorkflowRun：migration/comment/RunNow 同步触发、每任务 driver、还是常驻 outbox worker？ | 每任务单 driver；comment commit、migration、RunNow/resume 唤醒；startup 扫描未终态 runs。不加独立服务。 | Executor trigger |
| D2 | Agent 节点的 session identity 与 `session_reuse_policy`？ | 每 node session；仅显式 `task_main` 复用 Task main；iteration/attempt 策略从 node param 读取。 | session 隔离/成本 |
| D3 | 多个只读 NodeRun 同时活动时留言目标；零个 Agent NodeRun 活动时怎么处理？ | 唯一活动 NodeRun 时绑定；多个/零个时冲突或要求用户指定，不广播、不批准 HumanGate、不复活 terminal。客户端可传 expected node ID，服务端必须验证所属 run 且活动。 | comment routing |
| D4 | HumanGate approve/reject；普通评论是否批准？ | 专用 approve/reject；普通 comment 不隐式批准。 | 授权/UI |
| D5 | Agent structured JSON output 来源及缺失/解析失败行为？ | NodeRun JSON envelope；错误显式停住，不猜。 | Condition/Loop |
| D6 | Condition/Loop expression root：全图 outputs 还是 predecessor？ | 建立明确表达式语言；若采用 `node_id.output.*`，这是新增 grammar（后端 `workflow_validate.go` 有安全 evaluator；前端 `workflowModel.ts` 只有图 schema/default、无 expression mirror），需分别新增/测试两端实现。 | expression language |
| D7 | migrated task 旧 pause/resume/next/rerun/cancel/complete/fail 映射？ | pause 停止新 claim、等在途节点；cancel 有界中断活动 NodeRun；resume 唤醒同 Run；阶段专属动作逐项映射/禁用。 | board verbs |
| D8 | Restart auto-resume 的证据门槛？ | 有绑定、session 不在回复、terminal/done 可证、无未决副作用；否则 unknown/needs-user。 | recovery |
| D9 | queued/empty session request 何时可重排？FIFO continuation 的 NodeRun/attempt 身份？terminal run 是否可被评论重活？ | 仅有证据证明从未发送的 queued 可安全重排；其余 unknown。FIFO turn 独立 ID 且关联原 request；terminal 默认不复活，需显式 resume/new attempt。 | outbox/lifecycle |
| D10 | spec 已定写节点默认 per-Task durable gate；独立 worktree opt-in 与共享 physical tree gate owner/key？ | 默认 Task primary tree + per-Task gate；共享物理树必须 durable 互斥。无法提供则 fail closed，不声称满足 spec。 | worktree |
| D11 | merge target 与 branch owner conflict？ | Run 创建时解析实际项目/Task target 并快照；已由活动 NodeRun 占用的 branch 拒绝，不覆盖 owner。 | merge/registry |
| D12 | Agent tool surface 与可信 Agent identity 如何在 no-REST-auth 下实现？ | 获批 typed local tool/CLI→HTTP；若无可信 identity，Agent 只输出 patch，由人类 UI 导入；人类 publish revision 已是 spec 决定。 | agent authority |
| D13 | Append-only `(run_id,seq)` ledger 是否本期做？ | 最小 typed ledger；mutable Run 为 projection，测试 replay/recovery。 | persistence |
| D14 | Task 是否支持多个历史 WorkflowRun？当前 `task_id UNIQUE`。 | 至多一个 active，保留 terminal history/current binding；需 SQLite rebuild。 | schema/API |
| D15 | Gate 当前无生产 caller、Service per-account、gate 进程内；如何满足 durable per-Task writer gate？ | app 层共享 gate + durable claim/reconciliation；不能满足则禁止共享 worktree 并发写。 | cross-account safety |
| D16 | NodeRun→WorktreeEntry owner 如何持久化？当前 node schema无 worktree id/path，同 branch 可能覆盖 owner。 | NodeRun 或一对一关联表保存 owner；活动树唯一 NodeRun，冲突拒绝。 | recovery/merge |
| D17 | task node 缺少 Run snapshot Agent/harness/model？ | fail/needs-user，不回退其他配置。 | deterministic execution |

## 分层文件树与依赖

```text
docs/superpowers/plans/
├── 2026-11-10-kanban-workflow-runtime-roadmap.md
├── 2026-11-10-kanban-subagent-lane-isolation.md          # WP-00
├── 2026-11-10-kanban-workflow-runtime-executor.md
├── 2026-11-10-kanban-workflow-worktree-lifecycle.md
└── 2026-11-10-kanban-agent-first-workflow-management.md
```

| Wave | Plan | Decisions |
|---|---|---|
| 0 | WP-00：先定位和复现主/子会话 lane 现象 | 真正复现路径；合法 parent lifecycle card 保留；不能证明时 gate 不放行 |
| 1A | Executor persistence/outbox/driver | D1/D8/D9/D13/D14；统一 PostComment vs RecordComment writer、legacy NodeRun mapping |
| 1B | Runner port/session/Agent node | D2/D5/D17 |
| 1C | Condition/join/Loop/HumanGate/comment target | D3/D4/D6 |
| 1D | verbs/cancel/restart/retry/API/UI | D7–D9 |
| 2 | Worktree lifecycle | D10/D11/D15/D16；涉及共享文件须与 Executor 串行集成 |
| 3 | Agent-first management | D12；不得绕过 no-auth 项目规则；human publish confirmation 已定 |
| Final | 独立审查、登记和门禁 | 不授权 merge/deploy/restart |

**WP-00 gate rule:** 修复真实复现的 child-owned 内容串入，或由 reviewer 验证用户报告走其他明确 lane 且该 lane 已有独立复现/负责人后，才释放 Executor；单纯“当前 fake router test 没复现”不构成通过。

## Scope group 预分配

- WP-00：**G-LB（已登记）** —— 在线 Claude router 的 child-owned 工具卡归属是独立边界，`docs/upstream-customizations.yaml` 已写入症状/原因/边界/测试/anchors。其余候选在对应实施包完成后登记。
- Runtime：G-LC。
- Worktree：G-LE。G-KU 只覆盖 task/worktree 进程内 gate；G-KV 含 NodeRun lifecycle/merge 的既有意图，但不含新增 durable cross-account physical gate。实现前按实际 diff 重核。
- Agent-first：G-LD。
- G-KN stale-anchor repair：作为独立、获批的登记维护项处理；`web/capacitor.config.json` ignored/generated 且无 tracked anchor target，应改为 tracked invariant 并为 Capacitor tests 选 deterministic fixture/skip policy，不得把持续红作为永久 baseline。
- `docs/superpowers/plans/*.md` 由 G-A prefix 覆盖；`docs/plans/README.md` 后续需按 repo registration rules 处理。WP-00 的 G-LB 已登记；其余组在各自实现提交时登记。

## TDD 与验收门

1. 新行为先写 targeted failing test；已有 invariant 已由测试覆盖的，应扩展 coupling 而非复制同样断言。
2. 用 fake Runner/Session/SteerPort/SessionProbe/worktree adapter；不依赖真实 CLI/生产 DB。
3. Outbox 已测：`TestOutbox_SteerConfirmedOnlyRealReceipt`、`TestOutbox_TimeoutUnknownNoReplay`、`TestOutbox_FIFOSameSessionPreservesOrder`、`TestOutbox_RecordCommentIdempotent`、`TestEnsureOutboxTablesMatchesCanonicalContinueRequestsSchema`。新测聚焦绑定/生产接线/恢复，不重写已绿断言。
4. CAS 已测：`TestDraftCASAndPublishRevisionPin`、`TestRunRevisionCASAndFrozenNodes`。新增 edge freeze / proposal identity，别将 node/CAS 已测行为误标 RED。
5. WP-00 保留 `TestClaudeSubagentRouterRoutesTaskNotificationSummaryAndKeepsParentUpdate`、`TestClosedClaudeSubagentSwallowsFurtherUpdates`、`session_task_test.go` 合法 parent-card 行为；测试命令必须实际匹配所在 package。
6. WriterGate 当前未接入生产；Service per-account。Gate owner/key 改动需审查 `writer_gate_test.go` 与 G-KU anchors。
7. G-KN 目前 stale anchor `web/capacitor.config.json` 是 ignored/untracked，不能称为永久 baseline；单独规划 anchor 修复。Fresh worktrees 中 iOS shell generated config 可能不存在，测试需选择可靠 fixture/条件策略。
8. 文件硬线：`server/internal/kanban/service_outbox.go` 当前超过 500 行；后续编辑前先测试驱动职责拆分或将新增代码放入新文件。
9. `go.mod` 声明 Go 1.25.0、项目工具链 1.26.5；Go 命令在 repo root，Vitest 在 `web/`。
10. Executor 与 Worktree 有共享 TaskStore/Service 文件；指定单一 owner 串行整合，不并行改同文件。

## 风险与边界

- `integration/kanban-workflows` 分支未合入 main 且与 main 分叉；实施前重核拓扑并由用户批准同步策略。
- 用户截图是症状，不是 root-cause 证明；WP-00 只在 fake/replay 证据支持后修复。
- 当前 `PostComment` 与 OutboxService 不是生产闭环；两种 NodeRun 也未映射，须先确定 canonical writer/model。
- shared physical worktree 的 durable cross-account writer gate 与当前 per-account in-process Service 不相符，是安全阻断。
- no-auth 规则与 Agent-first 写控制面冲突须显式给可信边界，不准以 `author="agent"` 自声明冒充身份。
- 子计划所有实现、commit、merge、ship、service restart 均需另行授权；本文和子计划不构成授权。

## 子计划导航

1. [WP-00 主/子会话事件隔离](2026-11-10-kanban-subagent-lane-isolation.md)
2. [Workflow Runtime Executor](2026-11-10-kanban-workflow-runtime-executor.md)
3. [Workflow worktree lifecycle](2026-11-10-kanban-workflow-worktree-lifecycle.md)
4. [Agent-first workflow management](2026-11-10-kanban-agent-first-workflow-management.md)
