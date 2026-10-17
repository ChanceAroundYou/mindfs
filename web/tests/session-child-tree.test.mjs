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

test("both sidebar paths delegate child trees to the shared hook", () => {
  const single = read("components/session/SessionList.tsx");
  const multi = read("components/session/useMultiProjectSessionList.ts");
  assert.match(single, /useSessionChildTree\(/);
  assert.match(multi, /useSessionChildTree\(/);
  assert.match(multi, /selectedScope: \{ rootId: selectedRootId, nodeId: selectedNodeId \}/, "multi-project selection remains node-scoped");
  assert.match(multi, /initialExpandedChildren: expandedChildren/, "multi-project persisted expansion state is preserved");
});
