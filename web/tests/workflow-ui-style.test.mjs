import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

// Workflow surfaces must share the same panel design tokens as the task
// detail / template dialogs. The editor was hand-rolled with bare browser
// buttons, which made it look like a different product next to the board.
const buttonFiles = [
  "src/components/task/WorkflowDraftEditor.tsx",
  "src/components/task/WorkflowCanvas.tsx",
  "src/components/task/workflowAuthoring/WorkflowProposalReview.tsx",
  "src/components/task/workflowAuthoring/WorkflowLoopCanvas.tsx",
];

const inputFiles = [
  "src/components/task/WorkflowDraftEditor.tsx",
  "src/components/task/WorkflowCanvas.tsx",
  "src/components/task/workflowAuthoring/WorkflowLoopCanvas.tsx",
];

test("workflow UI uses shared panel styles", () => {
  const root = path.resolve(import.meta.dirname, "..");
  for (const rel of buttonFiles) {
    const src = fs.readFileSync(path.join(root, rel), "utf8");
    assert.match(src, /panelButtonStyle/, `${rel} must import panelButtonStyle`);
    let idx = src.indexOf("<button");
    while (idx !== -1) {
      const window = src.slice(idx, idx + 600);
      assert.match(
        window,
        /panelButtonStyle/,
        `${rel}: button without shared style near ${src.slice(idx, idx + 80)}`,
      );
      idx = src.indexOf("<button", idx + 1);
    }
  }
  for (const rel of inputFiles) {
    const src = fs.readFileSync(path.join(root, rel), "utf8");
    assert.match(src, /panelInputStyle/, `${rel} must use the shared input style`);
  }
  for (const rel of ["src/components/task/WorkflowDraftEditor.tsx", "src/components/task/WorkflowCanvas.tsx"]) {
    const src = fs.readFileSync(path.join(root, rel), "utf8");
    assert.match(src, /panelSurfaceStyle/, `${rel} must render cards with panelSurfaceStyle`);
  }
});