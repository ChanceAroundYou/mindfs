import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

test("file-operation-hooks", async () => {
const actions = fs.readFileSync(path.resolve(import.meta.dirname, "../src/hooks/useFileOperationActions.ts"), "utf8");
const move = fs.readFileSync(path.resolve(import.meta.dirname, "../src/hooks/useFileOperationMove.ts"), "utf8");
const component = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/file/FileOperations.tsx"), "utf8");

assert.match(actions, /confirmDialog/);
assert.match(actions, /promptDialog/);
assert.match(actions, /ProtectedAPIError/);
assert.match(actions, /body: JSON\.stringify\(\{ root, path, action, name \}\)/);
assert.match(move, /request\.current/);
assert.match(move, /action: "move", destination: dirs\.path/);
assert.match(component, /useFileOperationActions\(/);
assert.match(component, /useFileOperationMove\(/);
assert.doesNotMatch(component, /services\/(?:dialog|net\/api|net\/base|net\/error)/);

console.log("file operation hooks contract ok");
});
