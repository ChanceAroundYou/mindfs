# Workflow 多 worktree 生命周期接入 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. 此文仅是计划，不授权实现。

**Goal:** 将 workflow Agent NodeRun 的 workspace 声明接入现有 worktree registry、writer gate、自动合并和清理，使并行只读节点可并发、写入节点按获批 worktree 策略互斥，冲突时安全转人工处理。

**Architecture:** Workflow executor 只通过 Kanban Service 的明确生命周期端口 ensure/close/merge，不直接执行 git。节点启动前按节点参数与 D10 策略选择主工作树或独立工作树；每个 NodeRun 持久记录 worktree 身份；成功节点按策略合并，失败/取消/unknown 分别按确认后的保留语义处置。所有 merge target 在 run 创建时解析并快照，不允许运行时隐式落到字符串 `main`。

**Tech Stack:** Go module declares `go 1.25.0`, project toolchain is Go 1.26.5; `server/internal/kanban`, `gitview`, SQLite task-node DB.

**Spec:** `docs/superpowers/specs/2026-11-09-kanban-first-agent-workflows-design.md`；总路线图：`docs/superpowers/plans/2026-11-10-kanban-workflow-runtime-roadmap.md`；依赖 [Runtime Executor](2026-11-10-kanban-workflow-runtime-executor.md)。

## Global Constraints

- 先完成 WP-00 和 Runtime Executor Task 1–4，保证 NodeRun ID/state/run driver 有效。
- Gate key 按批准后的资源身份，至少同 worktree writer 串行；禁止只靠 task status 互斥。
- 不覆盖/清理 dirty 用户改动；merge conflict 保留两边 worktree/branch 并 needs-user，不自动 stash、abort 或解冲突。
- 不假定集成分支名为 `main`；分支目标必须显式解析并存入 Run snapshot。
- 单机内存 gate 不宣称跨进程互斥；重启依赖持久 NodeRun + registry 状态恢复。
- 本机服务不重启；不部署、不推送；每个改动遵守 customization / 测试 / Scope 规则。

## Review Focus

1. 同一个 task 由两个账户/Service 同时命中同一物理 worktree：不应因不同 worktree ID 绕过 writer gate。
2. 创建 worktree 后 NodeRun claim/启动失败：registry 不得留下无法识别的活跃孤儿；可幂等清理/恢复。
3. merge 成功但关闭目录失败：run 不得误标未合并，也不得重复 merge。
4. 主 checkout 当前分支不是 `main`：merge target 应是 run 中的显式集成分支，不能悄悄 merge 到 main。
5. 冲突或脏主树：两边文件保留，NodeRun/task 进入 needs-user，后续显式恢复有明确路径。

---

## Decisions / dependencies

- D10（默认共享主工作树还是 per-node tree）和 D11（integration branch identity）必须先由用户确认；本计划建议：默认共享 Task primary worktree；只有节点参数 `worktree=independent` 才独立建树；目标分支从项目实际 checkout 或 Task 显式配置解析，在 run snapshot 固定。
- Runtime NodeRun 是唯一节点身份来源；该 plan 不创建第二套 `node_runs` 事实表。
- 生命周期失败语义（Agent fail/unknown/cancel 时保留/关闭/合并）需与 Runtime Task 7 cancellation/recovery decision 对齐，不得自行推导。

## Files / ownership

- Modify `server/internal/kanban/service_worktree_noderun.go`：通过已实现 Ensure/Close API 提供幂等 NodeRun 生命周期。
- Modify `server/internal/kanban/service_worktree_merge.go`：传入已解析 integration branch；若禁止隐式默认，移除 `"main"` fallback，并在 workflow run 创建校验中要求 branch 已解析。
- Modify `server/internal/kanban/writer_gate.go` / `service.go`：WriterGate 当前未由 Service 持有，也无生产 Acquire caller；由 app 层决定/注入共享 gate registry，key 规范化 physical worktree path + branch。G-KU anchors/tests 如需改变，需登记互斥边界变更。
- Modify `server/internal/kanban/task_store_workflow_runs.go` or a dedicated association table：持久化 NodeRun 与 WorktreeEntry 关联；当前 workflow_node_runs schema 无 worktree_id/path，不可假设字段已存在。
- Modify `server/internal/kanban/worktree_registry.go`：同 branch 被不同活动 NodeRun 声明时明确 conflict，不可 `ON CONFLICT` 静默重写 owner。
- Modify workflow executor integration：传入 run-pinned branch 和 NodeRun↔WorktreeEntry 关联。与 Executor 子计划共享文件，须在同一集成波次指定单一文件 owner。
- Tests: `writer_gate_test.go`（保留/迁移当前不同 task key 独立获取 gate 的既有行为）、`service_worktree_multi_test.go`, `service_worktree_merge_test.go`, new `workflow_worktree_lifecycle_test.go`。

## Tasks

### Task 1：为工作流节点增加 workspace intent 和 gate RED tests

**Interfaces:** Consumes Runtime `WorkflowNodeExecution.NodeRun` + resolved D10/D11/D15/D16. Produces `AcquireNodeWriter(ctx, physicalWorktreeKey) (release func(), err error)` from the app-shared gate; final signature must be frozen by tests before implementation. Existing `WriterGate` has no production call site and is not yet a Service interface; inspect before reuse.

- [ ] Step 1: 写测试：`writes_workspace=false` 只读节点不取 writer gate；按 spec 默认共享 worktree 的两个写节点串行；不同经 D10 批准的独立 worktree 可并发。
- [ ] Step 2: 测试不同账户/Task Service 对同一 physical worktree 的并发请求；决策 D15 必须规定 gate owner/key 和 durable/跨进程范围。
- [ ] Step 3: Existing `task_worktrees.node_run_id` already stores owner, but `UpsertWorktreeEntry` currently overwrites it on `ON CONFLICT(task_id,worktree_id)`. Add a failing concurrent same-branch test; assert second NodeRun receives owner conflict and existing row remains unchanged (no new column is assumed).
- [ ] Step 4: 对比现有 `writer_gate_test.go` 的语义和 G-KU anchors；若 key/ownership 改动，明确迁移现有 tests/anchors 并注册 G-LE，不能破坏当前 documented task-scoped semantics 无说明。
- [ ] Step 5: `go test ./server/internal/kanban -run 'WriterGate|Workflow.*Writer' -count=1`; Expected: RED 来自新 workflow production integration/跨 Service test，而不是重写既有已绿单测。
- [ ] Step 6: 依据批准的 D15 把 gate 注入 app-shared Kanban Service dependencies；若只能单进程，跨进程必须明确不支持/检测冲突并 fail closed，不能宣称 durable per-Task semantics 已满足。

### Task 2：NodeRun start/finish 接线 Ensure/Close

**Interfaces:** Consumes Task 2 gate + Runtime `ClaimWorkflowNodeRun`. Produces lifecycle helper `PrepareWorkflowNodeWorktree(ctx, execution) (WorktreeEntry, release, error)` and `FinishWorkflowNodeWorktree(ctx, execution, outcome) error` (freeze names/signatures in failing tests after reviewing existing APIs).

- [ ] **Step 1: Extend NodeRun persistence tests**: existing `NodeRun`/`workflow_node_runs` do not contain `worktree_id/path`. Add migration + scan/write/reopen test for a one-to-one NodeRun↔WorktreeEntry association (columns or association table chosen under D16).
- [ ] **Step 2: Write failing prepare test**: independent Agent node claims/registers its worktree; NodeRun↔worktree association is durable and unique; same active branch requested by another NodeRun conflicts without changing registry owner.
- [ ] **Step 3: Test create failure**: no Runner call occurs and NodeRun enters explicit fail/needs-user state.
- [ ] **Step 4: Test success/failure/cancel/unknown close or retain semantics** exactly as resolved by D7–D9/D16; unknown must not delete a directory an agent may still be writing.
- [ ] **Step 5: Run tests to confirm RED, then wire `EnsureNodeWorktree` / `CloseNodeWorktree`**; do not duplicate owner truth outside NodeRun/approved association.
- [ ] **Step 6: Test repeated start/recovery** does not create a second worktree; Expected PASS.


### Task 3：成功 merge 与冲突 needs-user 路径接线

**Interfaces:** Consumes Task 2 terminal outcome + run-pinned integration branch. Produces deterministic merge result attached to NodeRun/activity.

- [ ] Step 1: 测试 NodeRun success + independent worktree → `AutoMergeWorktree` 恰调用一次、目标为 run snapshot integration branch。
- [ ] Step 2: 测试当前 checkout branch 非 `main` 时仍只使用 run snapshot 中显式集成分支；空分支配置必须在创建 Run 前失败，不能调用 merge。
- [ ] **Step 3: Test merge conflict**: registry `needs_user`, activity includes run/node/worktree/conflict files, NodeRun/Task project waiting_user; both worktree and source branch remain.
- [ ] **Step 4: Test dirty main checkout**: no stash/abort/overwrite/delete; explicit actionable error.
- [ ] **Step 5: Test merge success + close failure**: record merge success durably, close can retry without invoking a second merge.
- [ ] **Step 6: Add failing tests before connecting `AutoMergeWorktree`**; `IntegrationBranch` must be provided from Run snapshot and never fallback to implicit `main`.
- [ ] **Step 7: `go test ./server/internal/kanban -run 'Workflow.*(Merge|Conflict|Dirty)' -count=1`; Expected PASS.


### Task 4：节点生命周期端到端集成

**Files:** Modify `service_worktree_merge.go`, `service_worktree_noderun.go`, `writer_gate.go`, `service.go`, `worktree_registry.go`, `workflow_types.go`/`task_store_workflow_runs.go` for owner persistence, and app layer for shared gate injection. Tests include existing writer/worktree tests plus new workflow lifecycle integration tests.

- [ ] Step 1: Fake Agent workflow: queued→running→success→merge→close，断言 NodeRun、registry、commit target 与 Task status 一致。
- [ ] Step 2: Fake merge conflict: assert both branches remain and user-attention activity is queryable.
- [ ] **Step 3: Restart between NodeRun success and merge**: resume merge without re-running Agent and without duplicate merge commit.
- [ ] **Step 4: Run `go test -race ./server/internal/kanban`**; Expected: PASS.
- [ ] **Step 5: Check upstream boundaries** — G-KU explicitly covers only in-process task/worktree gate; G-KV's registered title/why includes NodeRun owner lifecycle and merge, so verify whether this is truly a new boundary or an extension of G-KV. Durable cross-account physical locking exceeds the existing G-KU boundary and must use G-LE; record the selected group and anchors in YAML.
- [ ] **Step 6: Future implementation commits use the confirmed Scope; this planning request authorizes no commit.**
