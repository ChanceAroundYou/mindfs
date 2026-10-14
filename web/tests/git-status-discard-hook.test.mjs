import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

test("git-status-discard-hook", async () => {
const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");
const view = read("src/components/git/GitStatusPanel.tsx");
const hook = read("src/components/git/useGitStatusPanel.ts");

assert.doesNotMatch(view, /services\/dialog/);
assert.match(view, /discardItem\(item, onDiscardItem\)/);
assert.match(hook, /confirmDialog\(\{/);
assert.match(hook, /item\.status !== "\?\?"/);
assert.match(hook, /t\("git\.confirmDeleteUntracked", \{ path: item\.path \}\)/);
assert.match(hook, /runPanelAction\(`discard:\$\{item\.path\}`/);

console.log("git status discard hook contract ok");
});
