import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

test("task-panel-service-boundaries", async () => {
const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

for (const file of [
  "src/components/app/SessionSheet.tsx",
  "src/components/app/TaskDetailPanelGate.tsx",
  "src/components/app/TaskInlineEditPanel.tsx",
]) {
  assert.doesNotMatch(read(file), /from ["'][^"']*services\//, `${file} should be driven by props and shared contracts`);
}

assert.match(read("src/components/app/TaskDetailPanelGate.tsx"), /shared\/taskTypes/);
assert.match(read("src/components/app/TaskInlineEditPanel.tsx"), /shared\/(agentTypes|candidateTypes|gitTypes|taskTypes|uploadTypes)/);
assert.match(read("src/components/app/SessionSheet.tsx"), /shared\/agentTypes/);

console.log("task panel service boundary contracts ok");
});
