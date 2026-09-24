import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

const sourcePath = path.resolve(import.meta.dirname, "../src/services/sessionLock.ts");
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

// 2026-09 App.tsx 拆分：会话右栏 JSX 移到 app/useSessionSidebarView.tsx，契约随文件走。
const sidebarView = fs.readFileSync(
  path.resolve(import.meta.dirname, "../src/app/useSessionSidebarView.tsx"),
  "utf8",
);
const app = fs.readFileSync(path.resolve(import.meta.dirname, "../src/App.tsx"), "utf8");
assert.match(app, /resolveLockedSessionKey\(activeBoundSessionKey\)/);
assert.match(sidebarView, /selectedKey=\{activeBoundSessionKey \|\| ""\}/);
assert.match(app, /shouldResetSessionLockForRootChange\(/);

// Positive source contracts added by Task 2 wiring.
assert.match(
  app,
  /const lockedSessionKey = resolveLockedSessionKey\(activeBoundSessionKey\);/,
  "ActionBar/send routing must derive from the canonical active bound lock",
);
assert.match(
  sidebarView,
  /selectedKey=\{activeBoundSessionKey \|\| ""\}/,
  "single-project list must highlight the lock even when the main view is a file",
);
assert.match(
  sidebarView,
  /selectedKey=\{activeBoundSessionKey \|\| ""\}[\s\S]*selectedRootId=\{currentRootId \|\| ""\}/,
  "multi-project list must highlight the active root lock",
);
assert.match(
  app,
  /setBoundSessionForRoot\(targetRoot, key\);/,
  "explicit sidebar selection must update the canonical lock",
);
assert.match(
  app,
  /if \(shouldResetSessionLockForRootChange\([\s\S]*?setBoundSessionForRoot\([^\n]+, null\);[\s\S]*?setDrawerSessionForRoot\([^\n]+, null\);/,
  "cross-project navigation must reset both source and target lock state",
);

// Negative contracts that protect the regression.
assert.doesNotMatch(
  app,
  /setMainViewPreferenceForRoot\(String\(root\), "file"\)[\s\S]{0,160}setBoundSessionForRoot\(String\(root\), null\)/,
  "same-project file navigation must not clear the bound lock",
);
assert.doesNotMatch(
  app,
  /setMainViewPreferenceForRoot\(root, "directory"\)[\s\S]{0,160}setBoundSessionForRoot\(root, null\)/,
  "same-project directory navigation must not clear the bound lock",
);

console.log("session-lock source contracts OK");
