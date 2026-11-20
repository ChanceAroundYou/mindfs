import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import React from "react";
import { renderToString } from "react-dom/server";
import { I18nProvider, translateNow } from "../src/i18n/index.tsx";
import { TaskCompletionChecklist } from "../src/components/task/TaskCompletionChecklist.tsx";
import {
  completionRows,
  completionStatusKey,
  completionVerdictKey,
} from "../src/components/task/taskCompletionModel.ts";

// 完成证据清单：回答「这一轮还差什么」。它是只读证据（清单 + 复核结论），
// 不提供任何操作。

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

const blocked = {
  present: true,
  status: "blocked",
  passed: false,
  items: [
    { index: 0, id: "build-done", label: "构建通过", kind: "node_output", pass: false, detail: "节点没有产出 build 键" },
    { index: 1, id: "all-done", label: "全部节点收尾", kind: "all_nodes_terminal", pass: true, detail: "" },
  ],
  repairs_used: 1,
  repairs_max: 2,
  verifier_verdict: "",
  updated_at: "2026-11-21T02:00:00Z",
};

const render = (completion) =>
  renderToString(
    React.createElement(I18nProvider, null, React.createElement(TaskCompletionChecklist, { completion })),
  );

test("numbered rows keep the report order and fall back to the id label", () => {
  const rows = completionRows(blocked);
  assert.equal(rows.length, 2);
  assert.equal(rows[0].number, 1);
  assert.equal(rows[1].number, 2);
  assert.equal(rows[0].pass, false);
  assert.equal(rows[0].detail, "节点没有产出 build 键");
  const unlabeled = completionRows({
    ...blocked,
    items: [{ index: 0, id: "file_exists:README.md", label: "", kind: "file_exists", pass: false, detail: "" }],
  });
  assert.equal(unlabeled[0].label, "file_exists:README.md");
});

test("verifier verdicts are labelled and unknown statuses stay honest", () => {
  assert.equal(completionVerdictKey("pass"), "task.completion.verdict.pass");
  assert.equal(completionVerdictKey("uncertain"), "task.completion.verdict.uncertain");
  assert.equal(completionVerdictKey(""), null);
  assert.equal(completionVerdictKey("made-up"), null);
  assert.equal(completionStatusKey("verifying"), "task.completion.status.verifying");
  assert.equal(completionStatusKey("weird"), null);
});

test("checklist renders numbered failures with their detail, and hides when empty", () => {
  assert.equal(render(null), "");
  const html = render(blocked);
  assert.match(html, /构建通过/, "the failing item label must be visible");
  assert.match(html, /1\./, "failing items must be numbered");
  assert.match(html, /节点没有产出 build 键/, "the detail explains what is missing");
  assert.match(html, /1 \/ 2/, "repairs budget is shown as used / max");
  assert.ok(!html.includes(translateNow("task.completion.modelVerdict")), "a contract failure is not a model verdict");
});

test("a verifier verdict is rendered with the model-judgement label", () => {
  const html = render({ ...blocked, status: "completed", verifier_verdict: "pass" });
  assert.ok(html.includes(translateNow("task.completion.modelVerdict")), "verifier output must be marked as a model judgement");
  assert.ok(html.includes(translateNow("task.completion.verdict.pass")), "the verdict itself is shown");
});

test("the entry fetches through services with the node id and stays out of the view layer", () => {
  const service = read("../src/services/task/taskCompletion.ts");
  assert.match(service, /appURL\(/, "requests must go through appURL");
  assert.match(service, /\/completion/, "requests must hit the completion endpoint");
  assert.match(service, /params, nodeId\)/, "requests must pass the node id to appURL (cross-node rule)");

  const checklist = read("../src/components/task/TaskCompletionChecklist.tsx");
  assert.doesNotMatch(checklist, /services\//, "view layer must not import services");

  const panel = read("../src/components/task/TaskDetailPanel.tsx");
  assert.match(panel, /<TaskCompletionEntry/, "the checklist entry must be mounted in the task detail panel");
  assert.match(
    panel,
    /<SupervisionEntry[\s\S]*<TaskCompletionEntry/,
    "the evidence checklist sits after the supervision log (both are read-only evidence rows)",
  );
});
