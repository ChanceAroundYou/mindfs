# Kanban-First Agent Workflows Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended). Steps use checkbox (`- [ ]`) syntax.

**Goal:** 将 MindFS 看板任务升级为 issue 式留言板协作 + 版本化工作流（Agent/Condition/HumanGate/Loop/Notify）、多 Agent 分派、运行时未触发子图可编辑、多 worktree 自动开启/合并，留言默认执行（steer 优先、FIFO 降级），单节点 unknown 不自动重试恢复。

**Architecture:** Kanban Task 为真相源；`TaskActivity`（留言板时间线）+ `ContinueRequest`（幂等执行请求）+ Agent Session 为执行介质；`WorkflowVersion`（不可变，控制面）/ `Draft`（CAS）/ `WorkflowRun`（任务快照，可编辑未触发部分）/ `NodeRun`（session 绑定）四层分离；Agent 段与运行视图从属于留言板下钻；`Session` 接口新增 `SteerCapability`（默认 FIFO），worktree 以 `(task_id, worktree_id)` 为 writer gate 粒度。

**Tech Stack:** Go 1.26.5 (server/internal/kanban, server/internal/agent), React 19 + TS 5.5 + Vite 5 (web/src), React Flow/xyflow (画布，阶段 5), SQLite（Task DB 每账户/节点）

**Spec:** docs/superpowers/specs/2026-11-09-kanban-first-agent-workflows-design.md

## Global Constraints

- 只支持字符串参数的 SessionSteerCapability；默认所有 Harness 走 FIFO，不以普通 SendMessage 冒充 steer。普通 SendMessage 活跃时排队。
- 不引入 Temporal/Hatchet/Redis/Postgres/独立调度服务；不接 GitHub Issue；不做跨节点接管。
- 新旧执行互斥：旧 Task 继续 legacy `Stages/StageRun`；显式迁移才冻结 legacy，不双写。存量任务不批量转换。
- WorkflowVersion 发布只新增版本，不原地改写；Draft 用 `base_revision` CAS；运行编辑用 run `revision` CAS，冲突返回 conflict。
- Unknown 永不自动重试；用户显式重试建新 attempt ID 并链接原记录，不声称 exactly-once。
- 不重启 `mindfs.service`（本机 system 单元，用户专权）；WSL/fn 重启可由 Agent 直接执行。
- 每次改动登记 `docs/upstream-customizations.yaml`，加针对性测试，提交信息标 `Scope: G-XXX`，跑 `make check-upstream` 对账。
- Go 提交 Conventional Commits（中文描述）。前端改完 `make build-web && make install`（不 embed）。
- 移动端工作流画布仅简单查看；桌面端完整编辑。

## 任务/阶段映射（Spec §实施阶段建议 → 工作包）

- 阶段 1「任务 Issue 化」→ WP-01 (A1 后端留言) + WP-02 (A2 前端留言板)
- 阶段 2「运行可靠性与 steer」→ WP-03 (A3 steer/Outbox/恢复)
- 阶段 3「工作流领域模型 + 多 worktree」→ WP-04 (A4 worktree/gate/合并) + WP-05 (A5 workflow store/engine)
- 阶段 4「Agent-first 工作流管理」→ WP-06 (A5 草稿/发布/Agent 编辑 API)
- 阶段 5「画布编辑与运行视图」→ WP-07 (A2 画布+运行视图)
- 阶段 6「未来 Router 扩展」→ 本期不做，预留接口

## Worktree 分配

每个 Agent 独立 worktree，分支命名 `agent/wp-XX-<slug>`：

| Agent | Worktree | 分支 | 拥有文件（独占写入） |
|---|---|---|---|
| A1 后端留言与执行请求 | `.worktree/agent-wp01-backend-comments` | `agent/wp01-backend-comments` | server/internal/kanban/{types.go（增补别名，只追加字段）,service_comments.go*,service_comment_store_test.go,task_store.go（新表 migrate），task_store_comments.go} + server/internal/api/http_task_comments.go |
| A2 前端留言板与执行段 | `.worktree/agent-wp02-frontend-board` | `agent/wp02-frontend-board` | web/src/components/task/CommentThread.tsx*,AgentRunSection.tsx*,useTaskComments.ts*,TaskDetailPanel.tsx（仅接线段）,useTaskDetailPanel.ts（仅接线段）,taskDetailUtils.ts（仅新增函数）+ web/tests/task-comments.test.mjs |
| A3 Steer/Outbox/恢复 | `.worktree/agent-wp03-steer-recovery` | `agent/wp03-steer-recovery` | server/internal/agent/types/session_type.go（SteerCapability）,server/internal/agent/pool.go（能力声明）,server/internal/kanban/service_outbox.go*,service_recover.go*,service_outbox_test.go |
| A4 多 Worktree/Writer Gate/自动合并 | `.worktree/agent-wp04-multi-worktree` | `agent/wp04-multi-worktree` | server/internal/kanban/service_worktree.go（改）,writer_gate.go*,service_worktree_merge.go*,worktree_registry.go*,对应 *_test.go |
| A5 Workflow 领域模型与管理后台 | `.worktree/agent-wp05-workflow-core` | `agent/wp05-workflow-core` | server/internal/kanban/workflow_types.go*,workflow_store.go*,workflow_engine.go*,workflow_validate.go*,template_store.go（改）,server/internal/api/http_workflows.go*,web 管理后台 workflow 页（新建 WorkflowAdmin*.tsx 用 A2 不占） |
| R1 评审/对账 | 主 checkout（只读+门禁） | `agent/r1-review`（若需补丁才建） | 只改 CHANGELOG、upstream-customizations.yaml、计划状态；不改业务代码 |

`*` = 新建文件。A1/A4 共享 `types.go`/`task_store.go`：A1 只追加新类型与表 migrate；A4 只在 `service_worktree.go` 同文件内函数级 append，不重叠行区间。A2 与 A5 前端文件不重叠。

## 工作包（每包可独立评审）

### WP-01 A1: 后端留言与执行请求
**Files:** 新建 `service_comments.go`（PostComment: 同事务写 Activity+ContinueRequest，`(task_id, comment_id)` 唯一，重复返回原记录，author/body 不符 conflict），`task_store_comments.go`（`task_activities`/`continue_requests` 表 + migrate），`http_task_comments.go`（POST/GET /api/tasks/{id}/comments, GET list）；改 `types.go`（只追加 `TaskActivity`/`ContinueRequest` 类型与 `CommentWorkflowChoice` 枚举）。
**Interfaces:** Produces `PostComment(ctx, in) (Activity, ContinueRequest, error)`；`ActivityType=user_comment/agent_reply/system_event/attention`；`Choice=指定/禁用/留空` 落库到 `workflow_choice` 列。
**验收:** 重复 ID 幂等不双跑；异体 conflict；留言默认产生 ContinueRequest；未消费请求终态 blocked/needs-user。

### WP-02 A2: 前端留言板与 Agent 执行段
**Files:** 新建 `CommentThread.tsx`（user↔agent 交替时间线 + 留言框含工作流三态选择），`AgentRunSection.tsx`（agent 段可展开→本段 NodeRun/session/结果，NodeRun 即 session 跳转），`useTaskComments.ts`（comment ID 客户端生成、重试复用，`appPath+nodeId` 显式传 nodeId），`web/tests/task-comments.test.mjs`；改 `TaskDetailPanel.tsx`/`useTaskDetailPanel.ts` 仅接线（上一文件头部“任务视图与留言板结构”层级）。
**Interfaces:** Consumes WP-01 的 HTTP API；调用 `POST /api/tasks/{id}/comments`。
**验收:** 留言板为默认核心视图；agent 段下钻；工作流三态必填可见；移动端留言可读。

### WP-03 A3: Steer/Outbox/恢复
**Files:** `server/internal/agent/types/session_type.go` 新增 `SteerCapability` 接口（`SupportsSteer() bool` + `Steer(ctx, sessionKey, msg) (SteerReceipt, error)`，`SteerReceipt=confirmed|sent|rejected|unknown`），默认所有 adapter 返回 `SupportsSteer=false`；`pool.go` 透传能力；新建 `service_outbox.go`（先提交 ContinueRequest/NodeRun+outbox 再消费，超时无法判定标 unknown 不重放），`service_recover.go`（重启读持久状态→查 adapter session/turn→可确定续跑否则 unknown/needs-user），`service_outbox_test.go`。
**验收:** 无回执不报 steer_confirmed；FIFO 同节点同 session 续聊；unknown 标记且不自动重试；新 attempt 链路。

### WP-04 A4: 多 Worktree / Writer Gate / 自动合并
**Files:** 改 `service_worktree.go`（注册表化：Task→多 worktree，归属 NodeRun，生命周期 自动创建/关闭），新建 `worktree_registry.go`（`(task_id, worktree_id, branch, node_run_id, state)` 表），`writer_gate.go`（gate key `(task_id, worktree_id)`，同 worktree 串行，不同 worktree 并行），`service_worktree_merge.go`（自动合并到集成分支，冲突转 needs-user 存 `merge_conflict` activity）。扩展 `task_store_worktree_refs.go` 引用计数为多 worktree。
**验收:** 节点声明独立 worktree 时自动创建分支；完成自动关闭；合并冲突进入 needs-user 不自动解；writer gate 粒度正确。

### WP-05 A5: Workflow 领域模型
**Files:** 新建 `workflow_types.go`（Workflow/WorkflowVersion/Draft/WorkflowRun/NodeRun/LoopIteration/边/条件表达式 AST），`workflow_store.go`（控制面存主节点，Version 不可变，Draft `base_revision` CAS，Run 快照 + `revision` CAS，未触发子图可编辑校验），`workflow_engine.go`（DAG 调度、条件选路、合流只等实际选中分支、Loop bounded `max_iterations` 1–20 默认 5 禁嵌套、只读并行/写入串行），`workflow_validate.go`（前缀可解释、未来路径可达、引用/条件有效、Loop 上限明确、删已引用节点拒绝）；改 `template_store.go`（模板 scope 全局/project 的 Draft API）；新建 `http_workflows.go`（新建任务时控制面内联 Version 快照到数据面，worker 不在线访问模板库）。
**验收:** 发布只新增版本；运行编辑 CAS conflict；Loop 超限 needs-user；合流 skipped 语义；legacy 互斥投影 Status。

### WP-06 A5: Agent-first 工作流管理（接 WP-05）
**Files:** 在 WP-05 上追加 Agent 草稿生成/改 API（增删未来节点/边、diff/验证/试运行），发布必须用户确认具体 revision ID；成功任务建议沉淀模板（不自动发布）。复用 WP-05 的 store/engine。
**验收:** Agent 改草稿/运行未触发部分；发布需显式确认；旧确认不能发布新内容。

### WP-07 A2: 画布与运行视图（接 WP-02）
**Files:** 桌面端 React Flow 画布编辑 Draft（节点五类、边、Loop 容器、dirty 红点、离开保存提示、验证错误）；移动端仅查看；运行视图时间线/树/泳道只读（留言板下钻）。
**验收:** 画布改 draft 产生新 revision；dirty 标记醒目；未保存离开提示。

## 依赖波次（可并行度）

- Wave 0（立即并行）: A1（无依赖） + A4（worktree 注册表/Gate 骨架，不依赖 A1 表）+ A5（workflow_types/store 骨架，不依赖 HTTP）。
- Wave 1（A1 完成后）: A2（依赖 WP-01 API）+ A3（依赖 ContinueRequest 表）+ A5 WP-06（依赖 WP-05 store）。
- Wave 2: A2 WP-07（依赖 WP-02 + WP-05 类型）+ 全量集成。
- R1 在 Wave 2 后统一对账、门禁、CHANGELOG。

## R1 评审 Agent 职责

- 跨 worktree diff 对账（文件所有权、接口一致性、Status 投影、writer gate 粒度）。
- 跑门禁：`make test`（Go）、`make build-web`（前端）、`make check-upstream`。
- 登记 `docs/upstream-customizations.yaml`（新组 G-XXX 互斥边界）、更新本计划状态、写 CHANGELOG 段。
- 失败/跳过/不确定如实汇报；不替业务 Agent 改代码（只出评审意见 + CHANGELOG）。

## 验收标准（10 条）

1. 留言板为任务详情核心，用户↔Agent 交替，发布时三态选择工作流。
2. 留言默认继续任务：steer 优先（有回执才 confirmed），否则同节点 FIFO 续聊；无回执不报已读。
3. 幂等：重复 HTTP/WS 不双跑，异体 conflict。
4. Agent 段/运行视图在留言板之下可下钻；NodeRun 跳转 session；复用/新建按 SessionReusePolicy。
5. 模板/运行分离：Version 不可变，Draft CAS，用户确认发布；运行未触发子图可编辑（revision CAS），已触发冻结。
6. Loop 显式 bounded（1–20 默认 5，不嵌套），超限 needs-user 保留结果。
7. 多 Agent 多 Harness/模型；只读并行，同 worktree 写串行，多 worktree 按 worktree 粒度并行，合并冲突 needs-user。
8. 控制面/数据面分离：Version 只在主节点，任务/运行在数据面，新建任务内联快照。
9. 恢复：单节点原 session 续跑；unknown 不自动重试，新 attempt 链路且提示副作用可能重复。
10. Legacy 互斥：旧任务 legacy 引擎，新建/显式迁移才走 WorkflowRun，不双写；Status 投影正确。

## Agent 提示词获取

主循环按本计划的「Worktree 分配 + 工作包」表，用 `Agent(isolation="worktree", subagent_type="general-purpose")` 派发。提示词必须含：工作包文件清单、接口签名、验收标准、Global Constraints、“不要改其他 Agent 拥有文件”。

## 执行状态（2026-11-09 记录）

| 工作包 | worktree | 提交 | 状态 |
|---|---|---|---|
| WP-01 后端留言/ContinueRequest | `agent-a649db859fc998fa3` | `b92bf7b9` | 完成（kanban+api 测试绿） |
| WP-02 前端留言板+三态 | `agent-ab4864584acd88735` | `80066620` | 完成（定向 vitest + build-web 绿） |
| WP-03 steer/outbox/启动恢复 | `agent-ad1a5797f0391b33f` | `0318e527` | 完成（新增 4 条集成测试，含变异验证） |
| WP-04 多 worktree/gate/合并 | `wp04-remaining` | `a53a8955` + `37a5df35` | 完成（含 race 子集） |
| WP-05/06 workflow core + HTTP | `agent-a8b177b002998b9ef` | `a571567d` + `3c10f159` | 部分完成：legacy 显式迁移入口未做 |
| WP-07 运行视图 RunTimeline | `agent-a56d3ffb1ba161187` | `defc3cd7` | 部分完成：画布编辑器未做 |

### 合入前必须处理的冲突

1. **自定义分组 ID 撞号**：WP-02、WP-04、WP-07 三个 worktree 各自都用了 `G-KR`。
   合入时重排：WP-02 → `G-KS`，WP-04 → `G-KU`，WP-07 → `G-KV`；WP-03 已用 `G-KT`。
2. **`docs/upstream-customizations.yaml` 合并**：各 worktree 都在文件尾部追加，需要按组做并集，不能整段取一边。
3. **`service.go` / `task_store_migrate.go` / `types.go`**：WP-01、WP-03、WP-04、WP-05 都碰过，需按「各自只追加自己的部分」逐段核对。
4. **`continue_requests` canonical DDL**：以 WP-01 的 `request_id` 主键为准；WP-03 已在 `EnsureOutboxTables` 对齐并加守卫测试。
5. **`make check-upstream` 存量红**：`G-KN` 锚点 `web/capacitor.config.json` 在基线 `a8321e5c` 就不存在，与本批改动无关。

### 集成分支（2026-11-09 收尾）

分支 `integration/kanban-workflows`（worktree `.claude/worktrees/integration`），
基点为 WP-03 tip，之后依次合并 WP-04 / WP-02 / WP-07，再补两块缺口：

- `713b2c0b` 存量任务显式迁移到 WorkflowRun（`task_execution_engines` 表 + 引擎互斥 +
  `POST /api/tasks/{id}/migrate-workflow`）；
- `d7896db6` 运行视图入口接到留言板下方（`RunViewEntry` + `GET /api/workflows/runs/{id}`
  与 `GET /api/tasks/{id}/engine`）；
- `11fbc6f7` 工作流草稿画布（`@xyflow/react ^12.12.1`）+ 前端同构校验；
- `f96ab581` 架构规则收口（视图层不 import services、去原生 select）；
- `3256f078` 草稿编辑器接入看板工具栏 + NodeRun 持久化出口：run/NodeRun 落任务节点
  SQLite（`workflow_runs` / `workflow_node_runs`），控制面 `WorkflowStore` 只留
  Draft/Version；`GET /api/tasks/{id}/workflow-run/nodes`；离开确认走应用内 dialog。

**门禁现状**（本 worktree 实测，含 `3256f078`）：`go test ./server/... ./cli/...` 全绿；
`make build-web` 通过；前端 vitest 213 文件通过、仅 2 个存量失败（`tests/ios-shell.test.mjs`
缺 `web/ios/App/App/capacitor.config.json`，基线 `a8321e5c` 就没有该文件）；
`make check-upstream` 仅剩存量 `G-KN` 锚点缺失。

### 仍未完成（按影响排序）

1. **工作流运行时执行器不存在（最大缺口）**：`workflow_engine.go` 只有纯状态机
   （`EngineReadyNodes` / `EngineEvaluateLoop` / `ProjectStatusFromRun`），没有生产调用方。
   迁移到 WorkflowRun 的任务因此**无人推进**：legacy 引擎被互斥挡住，也没有人按图启动
   agent session、写 `workflow_node_runs`、评估 Condition/合流、执行 HumanGate、把节点结果
   投影回任务状态。验收标准 5–9 的运行时部分全部落在这里。
2. **多 worktree 生命周期没有调用方**：`EnsureNodeWorktree` / `AutoMergeWorktree` /
   `CloseNodeWorktree` 只有测试在调；等执行器按节点 `writes_workspace` / worktree 声明接线。
3. **WP-06 Agent-first 工作流管理未做**：Agent 生成/改草稿、改运行未触发子图、diff/验证/
   试运行、成功任务沉淀模板建议——只有通用 Draft/Run CAS 接口，没有 Agent 侧入口。
4. **未合并回 `main`、未部署、未重启任何节点**（部署与重启由用户决定）。

