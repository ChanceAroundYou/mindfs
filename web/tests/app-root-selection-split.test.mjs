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

test("project root selection keeps cross-node cleanup in its state hook", () => {
  const layer = readPhysical("src/app/useAppStateLayer.ts");
  const selection = read("src/app/useAppRootSelection.ts");

  assert.ok(layer.split("\n").length - 1 < 762, "the state layer should shrink as selection logic moves out");
  assert.match(layer, /const selectRootNode = useAppRootSelection\([\s\S]*?getSessionListReloadTimerRef: \(\) => sessionListReloadTimerRef/);
  assert.match(selection, /getGitState: \(\) => Pick<GitState/);
  assert.match(selection, /sessionListAbortRef\.current\?\.abort\(\)[\s\S]*?kanbanAbortRef\.current\?\.abort\(\)/);
  assert.match(selection, /selectedSessionByRootRef\.current\[scopedRid\] = ""[\s\S]*?clearFileMemoryCacheForView\(\)[\s\S]*?clearGitHistoryCache\(rid, prevNid\)/);
  assert.match(selection, /managedRootByKeyRef\.current\[rootNodeKey\(nid, rid\)\]/);
  assert.match(read("tests/task-template-node-routing.test.mjs"), /selectRootNode must set the active node/);
});
