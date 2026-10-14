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
  assert.match(view, /from ["']\.\.\/\.\.\/shared\/taskTemplateConstants["']/);
  assert.match(view, /import type \{ KanbanTask, TaskTemplate \} from ["']\.\.\/\.\.\/shared\/taskTypes["']/);
  assert.match(view, /import type \{ WorkspaceBoard as WorkspaceBoardData, WorkspaceBoardFilter \} from ["']\.\.\/\.\.\/shared\/workspaceBoardTypes["']/);
  assert.match(view, /import type \{ SessionPresentationSummary \} from ["']\.\.\/\.\.\/shared\/sessionPresentationTypes["']/);
  assert.match(view, /import type \{ KanbanStageColumn, MoveKanbanAction, TaskSessionErrorDialog, TaskTemplateControls \} from "\.\/taskBoardTypes"/);
  // Task presentation assets live in the task view domain; the app facade is not imported here.
  assert.match(view, /<TaskBoardToolbar[\s\S]*?templateControls=\{templateControls\}/);
});

test("TaskBoard toolbar owns template-menu rendering and refresh feedback", () => {
  const toolbar = readFileSync(new URL("../src/components/task/TaskBoardToolbar.tsx", import.meta.url), "utf8");
  assert.match(toolbar, /data-onboarding="task-templates"/);
  assert.match(toolbar, /data-onboarding="task-refresh"/);
  assert.match(toolbar, /TaskTemplateActionMenu/);
  assert.match(toolbar, /TaskCreateTemplateMenu/);
  assert.match(toolbar, /useRefreshSpin\(onRefresh\)/);
});

test("appStorage preserves the public task-template filter facade", () => {
  assert.match(storage, /export \{ TASK_TEMPLATE_ALL_FILTER \} from ["']\.\.\/shared\/taskTemplateConstants["']/);
  assert.match(storage, /export \{ WORKSPACE_FILTERS \} from ["']\.\.\/shared\/workspaceBoardTypes["']/);
  assert.match(storage, /export type \{ WorkspaceBoardFilter \} from ["']\.\.\/shared\/workspaceBoardTypes["']/);
  assert.match(taskTypeFacade, /export \* from ["']\.\.\/\.\.\/shared\/taskTypes["']/);
  assert.match(workspaceLogic, /export type \{ WorkspaceBoard,[\s\S]*?\} from ["']\.\.\/shared\/workspaceBoardTypes["']/);
});
