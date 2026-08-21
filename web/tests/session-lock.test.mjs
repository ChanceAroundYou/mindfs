import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

const sourcePath = path.resolve("src/services/sessionLock.ts");
const source = fs.readFileSync(sourcePath, "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText;
const sandbox = { exports: {}, module: { exports: {} } };
vm.runInNewContext(compiled, sandbox, { filename: sourcePath });

const {
  normalizeSessionLockKey,
  resolveLockedSessionKey,
  shouldResetSessionLockForRootChange,
} = sandbox.exports;

assert.equal(normalizeSessionLockKey(" session-a "), "session-a");
assert.equal(normalizeSessionLockKey(""), null);
assert.equal(normalizeSessionLockKey(null), null);
assert.equal(normalizeSessionLockKey("pending-123"), "pending-123");
assert.equal(resolveLockedSessionKey("session-a"), "session-a");
assert.equal(resolveLockedSessionKey("pending-123"), undefined);
assert.equal(resolveLockedSessionKey(null), undefined);
assert.equal(shouldResetSessionLockForRootChange("root-a", "root-a"), false);
assert.equal(shouldResetSessionLockForRootChange("root-a", "root-b"), true);
assert.equal(shouldResetSessionLockForRootChange(null, "root-b"), false);

// Source contracts that will fail until Task 2 wires App.tsx to the pure model.
const app = fs.readFileSync(path.resolve("src/App.tsx"), "utf8");
assert.match(app, /resolveLockedSessionKey\(activeBoundSessionKey\)/);
assert.match(app, /selectedKey=\{activeBoundSessionKey \|\| ""\}/);
assert.match(app, /shouldResetSessionLockForRootChange\(/);
