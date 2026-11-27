import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "vitest";

const read = (relative) => fs.readFileSync(path.resolve(import.meta.dirname, "../src", relative), "utf8");
const hook = read("components/session/useSessionChildTree.ts");

test("shared child-tree hook owns the common tree state machine", () => {
  for (const state of ["expandedChildren", "loadingChildren", "childrenHasMore", "collapsedBaseCount"]) {
    assert.match(hook, new RegExp(`const \\[${state}, set`), `${state} must be owned by the shared hook`);
  }
  assert.match(hook, /parseForkSessionSource\(item\.source\)/, "fork sessions remain top-level");
  assert.match(hook, /children\.slice\(0, COLLAPSED_CHILD_SESSION_LIMIT\)/, "collapsed trees retain the three-child limit");
  assert.match(hook, /const collapsedBase = collapsedBaseCount\[key\] \?\? children\.length;/, "collapsed count uses the expansion baseline");
  assert.match(hook, /const toggleChild = async/, "pagination and collapse transition stay together");
});

test("running child sessions and their ancestors stay visible when collapsed", () => {
  // 在跑的子会话不能被「选中链路 + 前 3 条」埋掉：80 条子会话里活着的那条
  // 往往排在第 4 名之外，父会话没被选中时更是整段不渲染。
  assert.match(hook, /function isRunningSession/, "running predicate must exist");
  assert.match(hook, /item\.pending === true/);
  assert.match(hook, /item\.presence === "running"/);
  assert.match(hook, /item\.presence === "awaiting"/);
  assert.match(
    hook,
    /for \(const item of items\) \{\s*if \(!isRunningSession\(item\)\) continue;\s*activeParents\.add\(item\.key\)/,
    "running sessions and their ancestor chain must join activeParents",
  );
  assert.match(hook, /collapsedChildSessions\(children\)/, "collapsed view goes through the running-aware helper");
  assert.match(hook, /running = children\.filter/, "running children beyond the first N are appended");
  assert.match(
    hook,
    /Math\.max\(collapsedBase, children\.length\) - visible\.length/,
    "hidden count must not under-report children that arrived while collapsed",
  );
});

test("both sidebar paths delegate child trees to the shared hook", () => {
  const single = read("components/session/SessionList.tsx");
  const multi = read("components/session/useMultiProjectSessionList.ts");
  assert.match(single, /useSessionChildTree\(/);
  assert.match(multi, /useSessionChildTree\(/);
  assert.match(multi, /selectedScope: \{ rootId: selectedRootId, nodeId: selectedNodeId \}/, "multi-project selection remains node-scoped");
  assert.match(multi, /initialExpandedChildren: expandedChildren/, "multi-project persisted expansion state is preserved");
});
