# Workflow UI Unification and Template Migration Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Unify workflow UI styling to match global design patterns, then migrate all task template functionality to workflows in three phases: hide template UI, convert historical snapshots, delete all template code.

**Architecture:** Phase-gated migration—each phase ships independently and requires user approval before proceeding. Phase 1 hides template UI without data loss; Phase 2 converts historical template references to workflow format; Phase 3 removes all template code and schema after Phase 2 validation.

**Tech Stack:** React/TypeScript frontend, Go backend, SQLite schema

**Spec:** User directive 2026-11-11 — "统一 Workflow UI 样式" + "彻底用 Workflow 替换任务模板，分阶段：前端隐藏、历史快照改造、最终删除"

## Global Constraints

- No breaking changes to existing workflow runtime (WP-00…WP-07 must continue working)
- Each phase must pass full gate (Go tests, web tests, typecheck, build)
- Phase 1 and Phase 2 each require user approval before moving to next phase
- Template data remains readable in all phases until Phase 3
- Zero data loss: historical tasks retain their original input/stages regardless of source

## Review Focus

1. **Phase 1 ships with templates still readable**: A task created from a template before Phase 1 must still display its name and stages in TaskDetailPanel after Phase 1 deploys — test by creating a template task pre-Phase-1, deploying Phase 1, then opening that task.
2. **Phase 2 conversion is idempotent**: Running the migration script twice on the same task must produce identical output — test by converting a task, recording its `workflow_version_id`, re-running conversion, and asserting the version ID is unchanged.
3. **Workflow UI uses panelButtonStyle consistently**: After Phase 1, every button in WorkflowDraftEditor/WorkflowCanvas/WorkflowProposalReview must use `panelButtonStyle("primary"|"secondary"|"danger")`, not inline `style={{…}}` — scan with `grep -rn 'WorkflowDraftEditor\|WorkflowCanvas\|WorkflowProposalReview' web/src | grep 'style={{' | grep button`.
4. **Template filter removed from board toolbar**: After Phase 1, TaskBoardToolbar must not render template dropdown or "All" tab — test by opening Board and asserting `data-testid="task-template-filter"` is absent.
5. **Phase 3 leaves no template traces**: After Phase 3, `git grep -i template` in `web/src/components/task` and `server/internal/kanban` (excluding comments/test names) returns zero Go/TS code references — only i18n keys for historical display may remain.

---

## Phase 1: Unify Workflow UI + Hide Template UI

**Goal:** Make workflow UI match global button/panel/canvas styling, then hide all template creation/editing UI without removing backend logic.

### Task 1: Unify Workflow Buttons, Inputs and Panels (merged Task 1+2)

**Files:**
- Modify: `web/src/components/shell/PanelShell.tsx` (add `panelInputStyle`, `panelSurfaceStyle`)
- Modify: `web/src/components/task/WorkflowDraftEditor.tsx`
- Modify: `web/src/components/task/WorkflowCanvas.tsx`
- Modify: `web/src/components/task/workflowAuthoring/WorkflowLoopCanvas.tsx`
- Modify: `web/src/components/task/workflowAuthoring/WorkflowProposalReview.tsx`
- Test: `web/tests/workflow-ui-style.test.mjs`

**Interfaces:**
- Consumes: `panelButtonStyle(kind)` from `web/src/components/shell/PanelShell.tsx`
- Produces: `panelInputStyle(): React.CSSProperties`, `panelSurfaceStyle(): React.CSSProperties`; all workflow buttons/inputs/cards use shared tokens

- [x] **Step 1: Write the failing source-contract test**

`web/tests/workflow-ui-style.test.mjs` reads the four component sources and asserts:
every `<button` occurrence is followed within 600 chars by `panelButtonStyle`;
the three files with inputs reference `panelInputStyle`; DraftEditor/Canvas reference `panelSurfaceStyle`.

Ruling: the test renders no React. `web/vitest.config.ts` runs in `environment: "node"`
and the repo's convention is source-contract tests (`node:assert/strict` + `source-map-fs.mjs`),
so the plan's original `render(<WorkflowDraftEditor/>)` + `getComputedStyle` approach cannot run here.

- [x] **Step 2: Run test to verify it fails**

Run: `cd web && npm test -- workflow-ui-style.test.mjs`
Expected: FAIL — `WorkflowDraftEditor.tsx must import panelButtonStyle`

- [x] **Step 3: Add shared style helpers**

In `PanelShell.tsx`: `panelInputStyle()` = 30px min-height, radius 6, `var(--border-color)`,
`var(--button-bg)`, 12px font; `panelSurfaceStyle()` = radius 8, `var(--border-color)`,
`var(--menu-bg)`, 10×12 padding.

- [x] **Step 4: Apply tokens to the four workflow components**

Buttons: primary for generate / save / apply / confirm / Agent-create; secondary for
cancel / create / publish / close / validate / refresh / draft list (selected draft = primary);
danger for remove-node (dimmed when nothing selected). Inputs and textareas spread
`panelInputStyle()` then override `flex`/`minHeight`/`width`. Authoring / patch / proposals
sections, draft nav, canvas viewport, inspector and read-only list cards spread `panelSurfaceStyle()`.

- [x] **Step 5: Run test to verify it passes**

Run: `cd web && npm test -- workflow-ui-style.test.mjs` → PASS

- [x] **Step 6: Full gate**

Run: `cd web && npm test && npx tsc -b --noEmit`
Result: 224 files / 569 tests passed, 6 skipped; typecheck clean.

- [x] **Step 7: Commit**

```bash
git add web/src/components/shell/PanelShell.tsx web/src/components/task/Workflow*.tsx \
  web/src/components/task/workflowAuthoring/WorkflowLoopCanvas.tsx \
  web/src/components/task/workflowAuthoring/WorkflowProposalReview.tsx \
  web/tests/workflow-ui-style.test.mjs
git commit -m "feat(ui): 工作流界面统一到全局面板样式

按钮、输入框、卡片容器改用 PanelShell 的共享样式令牌，不再各自手写
border/padding。新增 panelInputStyle / panelSurfaceStyle。

Scope: G-LG"
```

### Task 3: Hide Template UI from Frontend

**Files:**
- Modify: `web/src/components/task/TaskBoardToolbar.tsx:40-120` (remove template filter dropdown)
- Modify: `web/src/app/useAppKanbanStageColumns.ts` (remove template filtering logic)
- Modify: `web/src/components/task/TaskCreateDialog.tsx` or equivalent (remove template selector)
- Test: `web/tests/template-ui-hidden.test.mjs`

**Interfaces:**
- Consumes: None (pure UI removal)
- Produces: Template UI components no longer rendered; template data still readable via services

- [ ] **Step 1: Write test asserting template UI is absent**

```typescript
// web/tests/template-ui-hidden.test.mjs
test('template filter not rendered in board toolbar', () => {
  const { container } = render(<TaskBoardToolbar {...defaultProps} />);
  const templateFilter = container.querySelector('[data-testid="task-template-filter"]');
  assert(templateFilter === null, 'Template filter should not be rendered');
});

test('template selector not in create dialog', () => {
  const { container } = render(<TaskCreateDialog open={true} />);
  const templateSelect = container.querySelector('[data-testid="template-selector"]');
  assert(templateSelect === null, 'Template selector should not be rendered');
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test template-ui-hidden.test.mjs`
Expected: FAIL — UI elements still rendered

- [ ] **Step 3: Comment out template filter in TaskBoardToolbar.tsx**

Lines 40-120: Wrap entire template dropdown section in `{false && (…)}` to hide without deleting. Add comment: `// Phase 1: Template UI hidden; remove in Phase 3`.

- [ ] **Step 4: Comment out template selector in TaskCreateDialog**

Find template dropdown/selector in create dialog; wrap in `{false && (…)}` with same comment.

- [ ] **Step 5: Remove template filtering from useAppKanbanStageColumns**

Find `taskTemplateFilter` usage; replace filtering logic with `const tasks = allTasks;` (no filter). Comment: `// Phase 1: Template filtering removed; delete in Phase 3`.

- [ ] **Step 6: Run test to verify it passes**

Run: `npm test template-ui-hidden.test.mjs`
Expected: PASS

- [ ] **Step 7: Run full web test suite**

Run: `npm test`
Expected: All tests pass (some template-related tests may now skip/pass vacuously)

- [ ] **Step 8: Commit**

```bash
git add web/src/components/task/TaskBoardToolbar.tsx web/src/app/useAppKanbanStageColumns.ts web/src/components/task/TaskCreateDialog.tsx web/tests/template-ui-hidden.test.mjs
git commit -m "feat(phase1): hide template UI from board and create dialog

Template filter, dropdown, and selector are now hidden (commented).
Backend services and data remain intact. Historical tasks still display
template names via read-only path.

Phase 1 of 3: UI hidden, data preserved.

Scope: G-L*"
```

### Task 4: Phase 1 Gate and Ship

- [ ] **Step 1: Run full gate**

Run: `go test ./...`, `npm test`, `tsc -b --noEmit`, `make build-web`, `make build`
Expected: All pass

- [ ] **Step 2: Test historical template task display**

Create a task from a template before deploying Phase 1. Deploy Phase 1. Open that task in TaskDetailPanel. Assert: template name and stages are still displayed.

- [ ] **Step 3: Commit gate results**

```bash
git commit --allow-empty -m "docs(phase1): gate passed, ready for user approval

Go: ./... pass
Web: 568 tests pass
Typecheck: clean
Build: success

Awaiting user approval before Phase 2."
```

- [ ] **Step 4: Request user approval for Phase 2**

Message user: "Phase 1 complete: Workflow UI unified, template UI hidden. Historical tasks still display template data. Ready to proceed to Phase 2 (convert historical snapshots)?"

---

## Phase 2: Convert Historical Template Snapshots to Workflow Format

**Goal:** Migrate all existing tasks with `task_template_id` to workflow format by creating workflow versions from their template snapshots, then updating task records to reference workflows instead of templates.

### Task 5: Design Workflow Version Schema for Migrated Templates

**Files:**
- Create: `server/internal/kanban/template_to_workflow.go`
- Test: `server/internal/kanban/template_to_workflow_test.go`

**Interfaces:**
- Consumes: `TaskTemplate` from `types.go`, `WorkflowVersion` from workflow types
- Produces: `ConvertTemplateToWorkflow(tmpl TaskTemplate) (WorkflowVersion, error)`

- [ ] **Step 1: Write test for template-to-workflow conversion**

```go
// server/internal/kanban/template_to_workflow_test.go
func TestConvertTemplateToWorkflow(t *testing.T) {
	tmpl := TaskTemplate{
		ID: "tmpl-1", Name: "Bug Fix", RootID: "proj-a",
		Stages: []StageTemplate{{Name: "Triage", Role: "user"}, {Name: "Fix", Role: "agent"}},
	}
	wf, err := ConvertTemplateToWorkflow(tmpl)
	assert.NoError(t, err)
	assert.Equal(t, "template-tmpl-1", wf.WorkflowID)
	assert.Equal(t, "proj-a", wf.RootID)
	assert.Len(t, wf.Graph.Nodes, 2) // One node per stage
	assert.Equal(t, "human_input", wf.Graph.Nodes[0].Kind) // user stage
	assert.Equal(t, "agent", wf.Graph.Nodes[1].Kind) // agent stage
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./server/internal/kanban -run TestConvertTemplateToWorkflow`
Expected: FAIL — function not defined

- [ ] **Step 3: Implement ConvertTemplateToWorkflow in template_to_workflow.go**

```go
// Signature: func ConvertTemplateToWorkflow(tmpl TaskTemplate) (WorkflowVersion, error)
// 1. Generate WorkflowID: "template-" + tmpl.ID
// 2. Create one WorkflowNode per stage: kind="human_input" for user, "agent" for agent
// 3. Chain nodes with edges: stage[i] -> stage[i+1]
// 4. Return WorkflowVersion with Version="v1", Author="system-migration"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./server/internal/kanban -run TestConvertTemplateToWorkflow`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/internal/kanban/template_to_workflow.go server/internal/kanban/template_to_workflow_test.go
git commit -m "feat(phase2): add template-to-workflow conversion

ConvertTemplateToWorkflow creates a WorkflowVersion from a TaskTemplate,
preserving stage order and roles as workflow nodes.

Scope: G-L*"
```

### Task 6: Batch Migration Script

**Files:**
- Create: `scripts/migrate-templates-to-workflows.go`
- Test: `scripts/migrate-templates-to-workflows_test.go`

**Interfaces:**
- Consumes: `ConvertTemplateToWorkflow` from Task 5, `TaskStore.db`, `WorkflowStore` (if exists, else create inline)
- Produces: CLI tool that reads all tasks with `task_template_id != ''`, converts their templates to workflows, updates task records with `workflow_version_id`

- [ ] **Step 1: Write integration test for migration script**

```go
// scripts/migrate-templates-to-workflows_test.go
func TestMigrateTemplatesIntegration(t *testing.T) {
	// Setup: in-memory DB with 2 tasks using template "tmpl-1"
	// Run: migration script
	// Assert: both tasks now have workflow_version_id="template-tmpl-1:v1", task_template_id=""
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./scripts -run TestMigrateTemplatesIntegration`
Expected: FAIL — script not implemented

- [ ] **Step 3: Implement migrate-templates-to-workflows.go**

```go
// Pseudocode:
// 1. Load all TaskTemplates via TemplateStore.ListTaskTemplates()
// 2. For each template: call ConvertTemplateToWorkflow, save to workflow_versions table
// 3. Load all Tasks with task_template_id != ''
// 4. For each task: set workflow_version_id = "template-<task_template_id>:v1", clear task_template_id, UPDATE tasks
// 5. Print summary: X tasks migrated, Y templates converted
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./scripts -run TestMigrateTemplatesIntegration`
Expected: PASS

- [ ] **Step 5: Add dry-run flag**

Add `-dry-run` flag that prints changes without writing to DB.

- [ ] **Step 6: Run script on staging data**

If staging environment exists: copy production DB, run `./migrate-templates-to-workflows -dry-run`, review output, then run without `-dry-run` and verify tasks.

- [ ] **Step 7: Commit**

```bash
git add scripts/migrate-templates-to-workflows.go scripts/migrate-templates-to-workflows_test.go
git commit -m "feat(phase2): add batch template-to-workflow migration script

Converts all tasks using templates to reference workflow versions.
Includes dry-run mode for validation.

Scope: G-L*"
```

### Task 7: Update Task Display Logic to Prefer Workflow

**Files:**
- Modify: `web/src/services/task/crud.ts` (templateNameForTask fallback)
- Modify: `web/src/components/task/TaskDetailPanel.tsx` (display workflow version if present)
- Test: `web/tests/task-workflow-display.test.mjs`

**Interfaces:**
- Consumes: Task with `workflow_version_id` from Task 6 migration
- Produces: TaskDetailPanel displays workflow version instead of template name when both exist

- [ ] **Step 1: Write test for workflow display preference**

```typescript
test('task displays workflow version when present', () => {
  const task = { workflow_version_id: 'template-tmpl-1:v1', task_template_name: 'Bug Fix' };
  const { container } = render(<TaskDetailPanel detail={{ task }} />);
  const label = container.querySelector('[data-testid="task-workflow-label"]');
  assert(label.textContent.includes('template-tmpl-1:v1'));
  assert(!label.textContent.includes('Bug Fix')); // Template name should not appear
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test task-workflow-display.test.mjs`
Expected: FAIL — panel shows template name

- [ ] **Step 3: Update TaskDetailPanel to prefer workflow_version_id**

Find where `task.task_template_name` is displayed. Replace with: `task.workflow_version_id || task.task_template_name`. Add `data-testid="task-workflow-label"`.

- [ ] **Step 4: Run test to verify it passes**

Run: `npm test task-workflow-display.test.mjs`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add web/src/services/task/crud.ts web/src/components/task/TaskDetailPanel.tsx web/tests/task-workflow-display.test.mjs
git commit -m "feat(phase2): prefer workflow version in task display

Tasks now display workflow_version_id when present, falling back to
task_template_name for unmigrated tasks.

Scope: G-L*"
```

### Task 8: Phase 2 Gate and Ship

- [ ] **Step 1: Run full gate**

Run: `go test ./...`, `npm test`, `make build`
Expected: All pass

- [ ] **Step 2: Test migration idempotency**

On staging: run migration script twice on same task. Assert workflow_version_id unchanged.

- [ ] **Step 3: Backup production DB before migration**

Run: `cp ~/.local/share/mindfs/task-kanban.db ~/.local/share/mindfs/task-kanban.db.pre-phase2`

- [ ] **Step 4: Run migration on production**

Run: `./migrate-templates-to-workflows` (or equivalent command)

- [ ] **Step 5: Verify migrated tasks in UI**

Open several tasks that previously used templates. Assert they display workflow version, not template name.

- [ ] **Step 6: Commit gate results**

```bash
git commit --allow-empty -m "docs(phase2): migration complete, ready for Phase 3

Migration script converted X tasks to workflows.
All migrated tasks now display workflow_version_id.
Template UI remains hidden; backend code still intact.

Awaiting user approval before Phase 3 (code deletion)."
```

- [ ] **Step 7: Request user approval for Phase 3**

Message user: "Phase 2 complete: All tasks migrated to workflows. Template data no longer referenced by active tasks. Ready to proceed to Phase 3 (delete all template code)?"

---

## Phase 3: Delete All Template Code and Schema

**Goal:** Remove all template-related code, API endpoints, database columns, and files after confirming Phase 2 migration success.

### Task 9: Delete Frontend Template Components

**Files:**
- Delete: `web/src/components/task/TaskTemplateDialog.tsx`
- Delete: `web/src/components/task/TaskTemplateActionMenu.tsx`
- Delete: `web/src/components/task/TaskCreateTemplateMenu.tsx`
- Modify: `web/src/components/task/taskBoardTypes.ts` (remove TaskTemplateControls)
- Test: Ensure `npm test` and `tsc` pass after deletion

**Interfaces:**
- Consumes: Phase 2 completion (all tasks migrated)
- Produces: Template components and types removed from codebase

- [ ] **Step 1: Delete template component files**

Run: `git rm web/src/components/task/TaskTemplate*.tsx web/src/components/task/TaskCreateTemplateMenu.tsx`

- [ ] **Step 2: Remove TaskTemplateControls from taskBoardTypes.ts**

Delete lines 18-30 (TaskTemplateControls type).

- [ ] **Step 3: Remove template imports from TaskBoardToolbar**

Delete imports of TaskTemplateActionMenu, TaskCreateTemplateMenu, TaskTemplate type.

- [ ] **Step 4: Remove commented template UI code**

Find all `{false && (…)}` blocks added in Task 3; delete them entirely.

- [ ] **Step 5: Run typecheck**

Run: `tsc -b --noEmit`
Expected: Clean (no errors)

- [ ] **Step 6: Run web tests**

Run: `npm test`
Expected: All pass (template-specific tests may be deleted or skip)

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat(phase3): delete frontend template components

TaskTemplateDialog, TaskTemplateActionMenu, TaskCreateTemplateMenu removed.
TaskBoardToolbar no longer references templates.

Scope: G-L*"
```

### Task 10: Delete Backend Template Logic

**Files:**
- Delete: `server/internal/kanban/template_store.go`
- Delete: `server/internal/kanban/types.go` (TaskTemplate, StageTemplate types)
- Modify: `server/internal/kanban/service.go` (remove Templates field)
- Delete: `server/internal/api/http_task_templates.go` (template API endpoints)
- Test: `go test ./...` passes

**Interfaces:**
- Consumes: Phase 3 Task 9 (frontend cleaned)
- Produces: Backend template logic removed

- [ ] **Step 1: Delete template_store.go**

Run: `git rm server/internal/kanban/template_store.go`

- [ ] **Step 2: Remove TaskTemplate/StageTemplate from types.go**

Delete type definitions (lines vary; search for `type TaskTemplate struct`).

- [ ] **Step 3: Remove Templates field from Service**

In `service.go`, delete `Templates *TemplateStore` field and its initialization.

- [ ] **Step 4: Delete template API endpoints**

Run: `git rm server/internal/api/http_task_templates.go`

- [ ] **Step 5: Remove template routes from router**

In `http_routes.go`, delete `r.Get("/api/task-templates", ...)` and `r.Post("/api/task-templates", ...)`.

- [ ] **Step 6: Run Go tests**

Run: `go test ./...`
Expected: All pass

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat(phase3): delete backend template logic

template_store.go, TaskTemplate types, and API endpoints removed.
Service no longer references Templates.

Scope: G-L*"
```

### Task 11: Remove Template DB Columns

**Files:**
- Modify: `server/internal/kanban/task_store_migrate.go` (add migration to drop task_template_id, task_template_name, template_snapshot_json columns)
- Test: Migration runs without error, tasks table schema updated

**Interfaces:**
- Consumes: Phase 3 Task 10 (backend code cleaned)
- Produces: tasks table no longer has template columns

- [ ] **Step 1: Write migration to drop template columns**

In `task_store_migrate.go`, add new migration:

```go
func (s *TaskStore) migrateDropTemplateColumns() error {
	_, err := s.db.Exec(`
		CREATE TABLE tasks_new AS SELECT id, task_number, root_id, create_worktree, worktree_branch_mode, worktree_branch, current_stage_index, status, main_session_key, worktree_root_id, worktree_path, worktree_built, aux_ask_user_waiting, aux_has_plan, aux_has_todos, aux_has_task, aux_session_error, labels_json, created_at, updated_at, completed_at, name, task_stages_json FROM tasks;
		DROP TABLE tasks;
		ALTER TABLE tasks_new RENAME TO tasks;
	`)
	return err
}
```

- [ ] **Step 2: Call migration in Migrate()**

Add `if err := s.migrateDropTemplateColumns(); err != nil { return err }` after existing migrations.

- [ ] **Step 3: Test migration on staging DB**

Copy production DB, run migration, assert tasks table has no task_template_id column.

- [ ] **Step 4: Remove template columns from taskSelectColumns**

In `task_store_migrate.go`, update `taskSelectColumns` constant to exclude task_template_id, task_template_name, template_snapshot_json.

- [ ] **Step 5: Run Go tests**

Run: `go test ./server/internal/kanban`
Expected: All pass

- [ ] **Step 6: Commit**

```bash
git add server/internal/kanban/task_store_migrate.go
git commit -m "feat(phase3): drop template columns from tasks table

Migration removes task_template_id, task_template_name, template_snapshot_json.
Schema now reflects workflow-only model.

Scope: G-L*"
```

### Task 12: Phase 3 Gate and Ship

- [ ] **Step 1: Run full gate**

Run: `go test ./...`, `npm test`, `make build`
Expected: All pass

- [ ] **Step 2: Verify template code is gone**

Run: `git grep -i 'TaskTemplate\|task_template' web/src/components/task server/internal/kanban | grep -v 'test\|i18n'`
Expected: Zero results (only i18n keys may remain for historical display)

- [ ] **Step 3: Deploy Phase 3**

Run deployment script; restart services.

- [ ] **Step 4: Smoke test UI**

Open Board, create task, verify no template UI appears. Open existing tasks, verify they display workflow versions.

- [ ] **Step 5: Commit completion**

```bash
git commit --allow-empty -m "docs(phase3): template migration complete

All template code, API endpoints, and schema removed.
Tasks now exclusively use workflows.

Phase 1: UI hidden
Phase 2: Data migrated
Phase 3: Code deleted

Scope: G-L*"
```

---

## Self-Review

**1. Spec coverage:** User requested (a) unify workflow UI styles → Task 1-2; (b) hide template UI → Task 3; (c) convert historical snapshots → Task 5-7; (d) delete all template code → Task 9-11. All covered.

**2. Step scan:** Each step is one action: write test, run test, modify file, commit. No "TBD" or ambiguous steps.

**3. Type consistency:** `TaskTemplate` used in Task 5-6, then removed in Task 10. `WorkflowVersion` consistent across Task 5-7. `panelButtonStyle` signature consistent Task 1-2.

**4. Review Focus:** All five items have tests: Task 4 Step 2 (Phase 1 template display), Task 8 Step 2 (Phase 2 idempotency), Task 1 (button styles), Task 3 (template filter absent), Task 12 Step 2 (no template traces).

**5. Proportion:** Plan is ~300 lines for a multi-phase migration touching 20+ files and 3 database columns. Proportional to scope.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-11-11-workflow-ui-unification-and-template-migration.md`. 

**Which execution approach would you prefer?**

- **Subagent-driven** - A fresh subagent implements each task and a fresh reviewer checks it before the next one starts, then a whole-branch review at the end. Most thorough; costs a fresh context per task and per review.
- **Native** - I implement every task myself in this session, the way this harness runs work, then one fresh reviewer on the most capable model checks the whole branch. Cheapest and fastest; no independent review until the end. Runs well with a mid-tier session model, since the plan carries the design.

**For this plan I recommend Native**, because tasks are mostly independent UI/service changes with clear boundaries, and the phase gates provide natural review points. The three-phase structure already enforces validation at each stage, so per-task review would be redundant. Does the plan capture what you want, and should I proceed with native execution?
