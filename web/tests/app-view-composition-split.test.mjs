import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import { test } from "vitest";

test("app-view-composition-split", async () => {
const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");
const parentPath = new URL("../src/app/useAppViewComposition.ts", import.meta.url);
const parent = readFileSync(parentPath, "utf8");
const types = read("../src/app/useAppViewCompositionTypes.ts");
const workspace = read("../src/app/useAppWorkspaceBoardComposition.ts");

assert.ok(parent.split("\n").length - 1 < 500, "the physical composition hook stays below the frontend hard line");
assert.match(parent, /export type \{ UseAppViewCompositionParams \} from "\.\/useAppViewCompositionTypes";/, "the old parameter type remains a compatibility export");
assert.match(types, /export type UseAppViewCompositionParams = \{/);
assert.match(parent, /useAppWorkspaceBoardComposition\(/);
assert.ok(parent.indexOf("useAppWorkspaceBoardComposition(") < parent.indexOf("useAppMainViewActions("), "workspace hook keeps its prior hook sequence position");
assert.ok(parent.indexOf("useAppMainViewActions(") < parent.indexOf("useAppKanbanTaskPanel("), "main-view actions still precede task panel composition");
assert.match(workspace, /const liveTasksById = useMemo\([\s\S]*?\}, \[taskDetailsById\]\);/, "live tasks remain state-derived so realtime updates invalidate the board");
assert.match(workspace, /getLiveTask = useCallback\(\(taskId: string\) => liveTasksById\[taskId\], \[liveTasksById\]\)/);
assert.match(workspace, /getNodeId: getNodeIdForRoot,[\s\S]*?fallbackNodeId: getNodeIdForRoot\(String\(currentRootIdRef\.current \|\| ""\)\) \|\| String\(getActiveNode\(\)\?\.id \|\| ""\)/, "fallback node routing is preserved");
assert.match(workspace, /workspaceBoardRefreshRef\.current = workspaceBoard\.refresh;/, "the board refresh bridge stays assigned during render");

console.log("app-view-composition-split.test.mjs: OK");
});
