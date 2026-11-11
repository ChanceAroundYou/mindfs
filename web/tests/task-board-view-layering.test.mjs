import { test } from "vitest";
import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";

const view = readFileSync(new URL("../src/components/task/TaskBoardView.tsx", import.meta.url), "utf8");
const storage = readFileSync(new URL("../src/app/appStorage.ts", import.meta.url), "utf8");
const taskTypeFacade = readFileSync(new URL("../src/services/task/types.ts", import.meta.url), "utf8");
const workspaceLogic = readFileSync(new URL("../src/app/useWorkspaceBoard.ts", import.meta.url), "utf8");
const valueImports = [...view.matchAll(/^import[\s\S]*?;/gm)]
  .map(([statement]) => statement)
  .filter((statement) => !/^import\s+type\b/.test(statement));

test("TaskBoardView avoids runtime app/storage dependencies", () => {
  assert.doesNotMatch(view, /from ["'][^"']*services\//);
  const appImports = valueImports.filter((statement) => /from ["'][^"']*app\//.test(statement));
  assert.equal(appImports.length, 0);
  assert.match(view, /from ["']\.\.\/\.\.\/hooks\/useResponsive["']/);
  assert.match(view, /import type \{ KanbanTask \} from ["']\.\.\/\.\.\/shared\/taskTypes["']/);
  assert.match(view, /import type \{ WorkspaceBoard as WorkspaceBoardData, WorkspaceBoardFilter \} from ["']\.\.\/\.\.\/shared\/workspaceBoardTypes["']/);
  assert.match(view, /import type \{ SessionPresentationSummary \} from ["']\.\.\/\.\.\/shared\/sessionPresentationTypes["']/);
  assert.match(view, /import type \{ KanbanStageColumn, MoveKanbanAction, TaskSessionErrorDialog \} from "\.\/taskBoardTypes"/);
  // 模板 UI 已下线（Phase 1）：看板视图不再接模板状态，创建入口直接交给工具栏。
  assert.doesNotMatch(view, /templateControls/);
  assert.match(view, /<TaskBoardToolbar[\s\S]*?openTaskCreateDialog=\{openTaskCreateDialog\}/);
});

test("TaskBoard toolbar owns create, workflow-entry and refresh controls", () => {
  const toolbar = readFileSync(new URL("../src/components/task/TaskBoardToolbar.tsx", import.meta.url), "utf8");
  assert.match(toolbar, /data-onboarding="task-create"/);
  assert.match(toolbar, /data-onboarding="task-refresh"/);
  assert.match(toolbar, /data-onboarding="task-workflow-editor"/);
  assert.match(toolbar, /useRefreshSpin\(onRefresh\)/);
  // 模板页签 / 模板动作菜单 / 按模板新建菜单已从工具栏下线（Phase 1）。
  assert.doesNotMatch(toolbar, /task-templates/);
  assert.doesNotMatch(toolbar, /TaskTemplateActionMenu/);
  assert.doesNotMatch(toolbar, /TaskCreateTemplateMenu/);
});

test("appStorage preserves the public task-template filter facade", () => {
  assert.match(storage, /export \{ TASK_TEMPLATE_ALL_FILTER \} from ["']\.\.\/shared\/taskTemplateConstants["']/);
  assert.match(storage, /export \{ WORKSPACE_FILTERS \} from ["']\.\.\/shared\/workspaceBoardTypes["']/);
  assert.match(storage, /export type \{ WorkspaceBoardFilter \} from ["']\.\.\/shared\/workspaceBoardTypes["']/);
  assert.match(taskTypeFacade, /export \* from ["']\.\.\/\.\.\/shared\/taskTypes["']/);
  assert.match(workspaceLogic, /export type \{ WorkspaceBoard,[\s\S]*?\} from ["']\.\.\/shared\/workspaceBoardTypes["']/);
});
