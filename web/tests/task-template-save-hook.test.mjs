import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const hook = fs.readFileSync(path.resolve(import.meta.dirname, "../src/hooks/useTaskTemplateSave.ts"), "utf8");
const dialog = fs.readFileSync(path.resolve(import.meta.dirname, "../src/components/task/TaskTemplateDialog.tsx"), "utf8");

assert.match(hook, /if \(!draft\.name\.trim\(\)\)/);
assert.match(hook, /draft\.stages\[0\]\.snapshot\.role !== "user"/);
assert.match(hook, /saveTaskTemplate\(/);
assert.match(hook, /root_id: scopeToCurrentProject \? \(currentRootId \|\| ""\) : ""/);
assert.match(hook, /setDraft\(cloneTemplate\(saved, t\)\)/);
assert.match(hook, /onSaved\?\.\(saved\)/);
assert.match(dialog, /useTaskTemplateSave\(/);
assert.doesNotMatch(dialog, /services\/(?:tasks|net\/error)/);

console.log("task template save hook contract ok");
