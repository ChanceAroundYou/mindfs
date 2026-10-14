import assert from "node:assert/strict";
import { closeSync, openSync, readFileSync } from "./source-map-fs.mjs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "vitest";

const root = resolve(fileURLToPath(import.meta.url), "../..");
const read = (path) => readFileSync(resolve(root, path), "utf8");
const readPhysical = (path) => {
  const fd = openSync(resolve(root, path), "r");
  try { return readFileSync(fd, "utf8"); } finally { closeSync(fd); }
};

test("session lifecycle delegates sync and list loading below the physical hard line", () => {
  const lifecycle = readPhysical("src/app/useSessionLifecycle.ts");
  const sync = read("src/app/useSessionSyncAction.ts");
  const lists = read("src/app/useSessionListLoading.ts");

  assert.ok(lifecycle.split("\n").length - 1 < 500, "lifecycle parent must be below the 500-line hard line");
  assert.match(lifecycle, /restoreActiveSession[\s\S]*?useSessionListLoading\(params\)[\s\S]*?useSessionSelection\([\s\S]*?handleRenameSession[\s\S]*?useSessionSyncAction\(params\)/);
  assert.match(sync, /isTransientExchange\(ex\)[\s\S]*?present\.has\(callId\)[\s\S]*?presentText\.has/);
  assert.match(sync, /exchanges: \[\.\.\.syncedExchanges, \.\.\.localTransientTail\]/);
  assert.match(lists, /const loadSessionsForRoot = useCallback[\s\S]*?sessionListLoadSeqRef[\s\S]*?const loadChildSessionsForParent = useCallback/);
  assert.match(lists, /_nodeId: childNodeId/);
  assert.doesNotMatch(lifecycle, /from ["'][^"']*components\//);
});
