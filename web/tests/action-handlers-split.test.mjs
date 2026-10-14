import assert from "node:assert/strict";
import { closeSync, openSync, readFileSync } from "./source-map-fs.mjs";
import { resolve } from "node:path";
import { test } from "vitest";

test("action-handlers-split", async () => {
const root = new URL("../", import.meta.url);
const readPhysical = (path) => {
  const fd = openSync(resolve(root.pathname, path), "r");
  try { return readFileSync(fd, "utf8"); } finally { closeSync(fd); }
};
const parent = readPhysical("src/app/useActionHandlers.ts");
const directory = readFileSync(new URL("../src/app/openDirectoryAction.ts", import.meta.url), "utf8");

assert.ok(parent.split("\n").length - 1 < 500, "the action handler coordinator stays below the frontend hard line");
assert.match(parent, /export type ActionHandlersParams = \{/ , "the parameter contract remains available from the original module");
assert.match(parent, /open_dir: createOpenDirectoryAction\(params, isMobile, t\)/, "directory action is created in the existing handlers memo");
assert.match(directory, /export function createOpenDirectoryAction/);
assert.match(directory, /await tryShowBoundSessionForRoot\(path/);
assert.match(directory, /await actionHandlersRef\.current\.open_dir\(/, "bound-session fallback still re-enters the same ref-backed handler");
assert.match(directory, /invalidTreeCacheKeysRef\.current\.delete\(cacheKey\)/);
assert.match(directory, /reportError\("file\.read_failed", message\)/);

console.log("action-handlers-split.test.mjs: OK");
});
