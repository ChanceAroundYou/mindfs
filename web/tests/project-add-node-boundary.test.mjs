import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

test("project add view reads node registry through its colocated logic hook", () => {
  const view = read("../src/components/file/ProjectAddPopover.tsx");
  const hook = read("../src/components/file/useProjectAddNodes.ts");
  const nodePanel = read("../src/components/account/NodeManagerPanel.tsx");
  const sessionContent = read("../src/components/app/SessionMainContent.tsx");
  assert.equal(view.includes("services/net/nodeRegistry"), false);
  assert.match(view, /useProjectAddNodes\(\)/);
  assert.match(hook, /getNodes\(\)/);
  assert.match(nodePanel, /shared\/nodePalette/);
  assert.match(sessionContent, /shared\/gitTypes/);
  assert.equal(sessionContent.includes("services/git/types"), false);
});
