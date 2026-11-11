import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import nodeFs from "node:fs";
import path from "node:path";
import { test } from "vitest";
import { defaultTaskFlowStages } from "../src/app/defaultTaskFlow";

// Phase 3 of the template→workflow migration: task templates are gone. The
// built-in user→agent flow is now a plain StageTemplate[] — no id, no store,
// no template picker. These checks are the absence guard: if a template module
// or a `taskTemplate.` i18n key comes back, the suite fails.

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

function sourceFiles(dir) {
  const out = [];
  for (const entry of nodeFs.readdirSync(path.join(root, dir), { withFileTypes: true })) {
    const rel = path.join(dir, entry.name);
    if (entry.isDirectory()) out.push(...sourceFiles(rel));
    else if (/\.(ts|tsx)$/.test(entry.name)) out.push(rel);
  }
  return out;
}

test("built-in default flow is a plain user stage followed by an agent stage", () => {
  const stages = defaultTaskFlowStages();
  assert.equal(stages.length, 2);
  assert.equal(stages[0].role, "user");
  assert.equal(stages[1].role, "agent");
  assert.equal(stages[1].prompt_template, "{previous_input}");
  assert.equal("snapshot" in stages[0], false, "stages are flattened, not template snapshots");
});

test("template modules and services are deleted", () => {
  const gone = [
    "src/app/useTaskTemplates.ts",
    "src/app/useAppTaskTemplateState.ts",
    "src/hooks/useTaskTemplateSave.ts",
    "src/components/task/TaskTemplateDialog.tsx",
    "src/components/task/TaskCreateTemplateMenu.tsx",
    "src/components/task/TaskTemplateActionMenu.tsx",
    "src/services/task/templates.ts",
    "src/shared/taskTemplateConstants.ts",
  ];
  for (const rel of gone) {
    assert.equal(nodeFs.existsSync(path.join(root, rel)), false, `${rel} must be deleted`);
  }
});

test("no source file mentions task templates", () => {
  // task_template_name / task_template_id are frozen historical columns; they
  // may still be read for display, but nothing may build a template feature on them.
  const allowed = /task_template_(name|id)|template_snapshot_json|legacyFlowFromTemplate|task_template_json/;
  const offenders = [];
  for (const rel of sourceFiles("src")) {
    read(rel).split("\n").forEach((line, index) => {
      if (/TaskTemplate|taskTemplate\.|taskStagesForCreate\(template|fetchTaskTemplates|saveTaskTemplate|deleteTaskTemplate/.test(line) && !allowed.test(line)) {
        offenders.push(`${rel}:${index + 1}: ${line.trim()}`);
      }
    });
  }
  assert.deepEqual(offenders, [], "template feature code must not exist outside the frozen columns");
});

test("creating a task no longer sends or tracks a template id", () => {
  const composer = read("src/app/useTaskComposer.ts");
  assert.doesNotMatch(composer, /templateId|templateName|taskTemplates/);
  assert.match(composer, /defaultTaskFlowStages\(\)/);
  const crud = read("src/services/task/crud.ts");
  assert.doesNotMatch(crud, /task_template_id|task_template_name/);
  assert.match(crud, /createTask\(\n  rootId: string,/);
});

test("the create button opens the dialog without a template argument", () => {
  const toolbar = read("src/components/task/TaskBoardToolbar.tsx");
  assert.match(toolbar, /openTaskCreateDialog\(currentRootId\)/);
  assert.doesNotMatch(toolbar, /template/i);
});

test("no template i18n keys remain", () => {
  for (const rel of ["src/i18n/locales/zh-CN-tasks.ts", "src/i18n/locales/en-US-tasks.ts"]) {
    assert.doesNotMatch(read(rel), /"taskTemplate\./, `${rel} must not keep taskTemplate.* keys`);
  }
});
