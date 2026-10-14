import { expect, test } from "vitest";
import { readFileSync } from "./source-map-fs.mjs";
import { findTaskProjectRoot, parseTaskProjectScopeKey, taskProjectScopeKey } from "../src/app/appTask.ts";

const appTask = readFileSync(new URL("../src/app/appTask.ts", import.meta.url), "utf8");
const appStorage = readFileSync(new URL("../src/app/appStorage.ts", import.meta.url), "utf8");
const useTaskComposer = readFileSync(new URL("../src/app/useTaskComposer.ts", import.meta.url), "utf8");
const taskInlineEditPanel = readFileSync(new URL("../src/components/app/TaskInlineEditPanel.tsx", import.meta.url), "utf8");

test("task worktree root lookup is scoped by node when project IDs collide", () => {
  const local = { id: "same-project", _nodeId: "node-local", is_git_repo: false };
  const remote = { id: "same-project", _nodeId: "node-remote", is_git_repo: true };
  const byId = { "same-project": local };
  const byKey = {
    [taskProjectScopeKey("same-project", "node-local")]: local,
    [taskProjectScopeKey("same-project", "node-remote")]: remote,
  };

  expect(findTaskProjectRoot("same-project", "node-remote", byKey, byId)).toBe(remote);
  expect(findTaskProjectRoot("same-project", "missing-node", byKey, byId)).toBeUndefined();
  expect(taskProjectScopeKey("same-project", "node-local")).not.toBe(
    taskProjectScopeKey("same-project", "node-remote"),
  );
});

test("project scope keys round trip root and node IDs", () => {
  const key = taskProjectScopeKey("same-project", "node-remote");
  expect(parseTaskProjectScopeKey(key)).toEqual({ rootId: "same-project", nodeId: "node-remote" });
  expect(parseTaskProjectScopeKey("local-project")).toEqual({ rootId: "local-project", nodeId: "" });
});

test("workspace task creation carries the selected node through the shared dialog", () => {
  expect(useTaskComposer).toMatch(
    /const openTaskCreateDialog = useCallback\(\(template: TaskTemplate \| null, targetRootId\?: string, targetNodeId\?: string\)/,
  );
  expect(appTask).toMatch(/targetNodeId\?: string/);
  expect(useTaskComposer).toMatch(/findTaskProjectRoot\(rootId, nodeId,/);
  expect(useTaskComposer).toMatch(/edit\.targetNodeId \|\| getNodeIdForRoot\(rootId\)/);
  expect(useTaskComposer).toMatch(/createTask\([\s\S]{0,300}?nodeId,/);
  expect(taskInlineEditPanel).toMatch(/findTaskProjectRoot\(taskInlineTargetRootId, taskInlineTargetNodeId/);
  expect(taskInlineEditPanel).toMatch(/value=\{taskProjectScopeKey\(taskInlineTargetRootId, taskInlineTargetNodeId\)\}/);
  expect(appStorage).toMatch(/loadTaskCreateWorktreePreference\(rootId: string, nodeId\?: string\)/);
  expect(appStorage).toMatch(
    /saveTaskCreateWorktreePreference\(rootId: string, pref: TaskCreateWorktreePreference, nodeId\?: string\)/,
  );
});
