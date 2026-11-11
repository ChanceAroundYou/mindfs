import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";
import { DEFAULT_TASK_FLOW_ID, defaultTaskFlowTemplate } from "../src/app/defaultTaskFlow";

// Phase 1 of the template→workflow migration: the template UI is invisible, but
// task creation must keep working. Creation used to require a selected template;
// it now falls back to one built-in user→agent flow that never touches the
// template store (so it also works on worker nodes, where templates do not exist).

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

test("built-in default flow is a user stage followed by an agent stage", () => {
  const flow = defaultTaskFlowTemplate();
  assert.equal(flow.id, DEFAULT_TASK_FLOW_ID);
  assert.equal(flow.stages.length, 2);
  assert.equal(flow.stages[0].snapshot.role, "user");
  assert.equal(flow.stages[1].snapshot.role, "agent");
  assert.equal(flow.stages[1].snapshot.prompt_template, "{previous_input}");
});

test("board toolbar no longer renders template tabs or template menus", () => {
  const src = read("src/components/task/TaskBoardToolbar.tsx");
  assert.doesNotMatch(src, /TaskCreateTemplateMenu/, "toolbar must not render the template picker");
  assert.doesNotMatch(src, /TaskTemplateActionMenu/, "toolbar must not render the template action menu");
  assert.doesNotMatch(src, /role="tablist"/, "toolbar must not render template tabs");
  assert.match(src, /openTaskCreateDialog\(null/, "create button must open the dialog without a template");
});

test("board no longer gates on a template filter", () => {
  const src = read("src/components/task/TaskBoardView.tsx");
  assert.doesNotMatch(src, /createTemplateFirst/, "board must not ask the user to create a template first");
  assert.doesNotMatch(src, /isAllTaskTemplateFilter/);
});

test("task list is no longer filtered by template", () => {
  const src = read("src/app/useTaskBoard.ts");
  assert.doesNotMatch(src, /taskTemplateFilter/, "task list must include every task");
});

test("create dialog no longer offers a template picker", () => {
  const src = read("src/components/app/TaskInlineEditPanel.tsx");
  assert.doesNotMatch(src, /task\.selectTemplate/, "create dialog must not render the template select");
  assert.match(src, /defaultTaskFlowTemplate/, "create dialog must fall back to the built-in flow");
});

test("workspace quick launch no longer needs a template", () => {
  const src = read("src/components/workspace/WorkspaceQuickLaunch.tsx");
  assert.doesNotMatch(src, /TaskTemplate/);
  assert.doesNotMatch(src, /templates/);
});

test("inline-edit save always synthesizes stages from the built-in flow", () => {
  const src = read("src/app/useTaskComposer.ts");
  assert.match(src, /defaultTaskFlowTemplate/, "save must resolve the built-in flow when no template is selected");
});