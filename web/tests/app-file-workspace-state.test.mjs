import assert from "node:assert/strict";
import { closeSync, openSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";

const root = resolve(fileURLToPath(import.meta.url), "../..");
const read = (path) => readFileSync(resolve(root, path), "utf8");
const readPhysical = (path) => {
  const fd = openSync(resolve(root, path), "r");
  try { return readFileSync(fd, "utf8"); } finally { closeSync(fd); }
};

test("state layer delegates the contiguous file and workspace state group", () => {
  const layer = readPhysical("src/app/useAppStateLayer.ts");
  const workspace = read("src/app/useAppFileWorkspaceState.ts");

  assert.ok(layer.split("\n").length - 1 < 714, "the state layer should shrink as state groups move out");
  assert.match(layer, /const \{[\s\S]*?editDraftRequest,[\s\S]*?worktreeStatusLoadingByPath,[\s\S]*?\} = useAppFileWorkspaceState\(/);
  assert.match(layer, /useAppFileWorkspaceState\([\s\S]*?refreshTaskWorktreeRef,[\s\S]*?\}\);[\s\S]*?useAppRefSync\(/);
  assert.match(workspace, /useGitData\(/);
  assert.match(workspace, /useProjectTreeWorktrees\(/);
  assert.match(workspace, /loadPersistedFileScrollPositions|TREE_SORT_STORAGE_KEY/);
  assert.match(workspace, /mainEntriesRef\.current = mainEntries/);
  assert.match(workspace, /refreshTaskWorktreeRef\.current = refreshTaskWorktree/);
});
