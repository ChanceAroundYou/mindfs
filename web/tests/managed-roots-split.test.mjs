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

test("managed root rename remapping has a dedicated hook below the physical hard line", () => {
  const roots = readPhysical("src/app/useManagedRoots.ts");
  const rename = read("src/app/useManagedRootRename.ts");
  const types = read("src/app/useManagedRootsTypes.ts");

  assert.ok(roots.split("\n").length - 1 < 500, "managed root coordinator must be below the 500-line hard line");
  assert.match(roots, /useManagedRootRename\(params\)[\s\S]*?handleRenameCurrentRoot/);
  assert.match(rename, /remapSessionCacheKey[\s\S]*?moveCacheRecord\(sessionCacheRef\.current\)[\s\S]*?moveTreeRecord\(entriesByPathRef\.current\)/);
  assert.match(rename, /managedRootByIdRef\.current = nextRootById[\s\S]*?setRootNodeMap/);
  assert.match(types, /export type UseManagedRootsParams/);
  assert.doesNotMatch(roots, /from ["'][^"']*components\//);
});
