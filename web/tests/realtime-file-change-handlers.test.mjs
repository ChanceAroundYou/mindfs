import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { MODULES } from "./source-map.mjs";

const read = (path) => readFileSync(new URL(`../${path}`, import.meta.url), "utf8");
const realtime = read("src/app/useRealtimeEvents.ts");
const handlers = read("src/app/realtimeWsFileChangedHandlers.ts");

assert.match(handlers, /export function createRealtimeWsFileChangedHandlers/);
assert.match(handlers, /return \{ handleFileChanged, handleFileChangedBatch \}/);
assert.match(handlers, /invalidateFileCache\(rootID, path\)/);
assert.match(handlers, /invalidatePluginsForRoot\(rootID\)/);
assert.match(handlers, /refreshCurrentFileContent\(rootID, currentFile\.path\)/);
assert.match(handlers, /buildGitDiffCacheSignature\(target\)/);
assert.match(handlers, /invalidTreeCacheKeysRef\.current\.add\(treeCacheKey\(rootID, dir\)\)/);
assert.match(realtime, /\.\.\.fileChangedHandlers/);
assert.ok(
  MODULES["src/app/useRealtimeEvents.ts"].includes("src/app/realtimeWsFileChangedHandlers.ts"),
  "the logical useRealtimeEvents source must include the extracted file event module",
);

console.log("realtime-file-change-handlers.test.mjs: OK");
