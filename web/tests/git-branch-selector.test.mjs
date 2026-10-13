import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

test("git status delegates branch menu presentation while retaining fetch and switch state", () => {
  const panel = read("../src/components/git/GitStatusPanel.tsx");
  const selector = read("../src/components/git/GitBranchSelector.tsx");
  assert.match(panel, /<GitBranchSelector[\s\S]{0,900}?setBranchMenuOpen\(false\)/);
  assert.match(panel, /await onSwitchBranch\?\.\(branchName\)/);
  assert.match(selector, /t\("git\.loading"\)/);
  assert.match(selector, /t\("git\.noBranches"\)/);
  assert.match(selector, /disabled=\{active \|\| !!switchingBranch\}/);
  assert.match(selector, /onClick=\{\(\) => void onSwitch\(item\.name\)\}/);
});
