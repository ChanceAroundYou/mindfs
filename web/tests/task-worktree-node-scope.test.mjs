import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
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

  assert.equal(findTaskProjectRoot("same-project", "node-remote", byKey, byId), remote);
  assert.equal(findTaskProjectRoot("same-project", "missing-node", byKey, byId), undefined);
  assert.notEqual(
    taskProjectScopeKey("same-project", "node-local"),
    taskProjectScopeKey("same-project", "node-remote"),
  );
});

test("project scope keys round trip root and node IDs", () => {
  const key = taskProjectScopeKey("same-project", "node-remote");
  assert.deepEqual(parseTaskProjectScopeKey(key), { rootId: "same-project", nodeId: "node-remote" });
  assert.deepEqual(parseTaskProjectScopeKey("local-project"), { rootId: "local-project", nodeId: "" });
});

test("workspace task creation carries the selected node through the shared dialog", () => {
  assert.match(
    useTaskComposer,
    /const openTaskCreateDialog = useCallback\(\(template: TaskTemplate \| null, targetRootId\?: string, targetNodeId\?: string\)/,
  );
  assert.match(appTask, /targetNodeId\?: string/);
  assert.match(useTaskComposer, /findTaskProjectRoot\(rootId, nodeId,/);
  assert.match(useTaskComposer, /edit\.targetNodeId \|\| getNodeIdForRoot\(rootId\)/);
  assert.match(useTaskComposer, /createTask\([\s\S]{0,300}?nodeId,/);
  assert.match(taskInlineEditPanel, /findTaskProjectRoot\(taskInlineTargetRootId, taskInlineTargetNodeId/);
  assert.match(taskInlineEditPanel, /value=\{taskProjectScopeKey\(taskInlineTargetRootId, taskInlineTargetNodeId\)\}/);
  assert.match(appStorage, /loadTaskCreateWorktreePreference\(rootId: string, nodeId\?: string\)/);
  assert.match(appStorage, /saveTaskCreateWorktreePreference\(rootId: string, pref: TaskCreateWorktreePreference, nodeId\?: string\)/);
});
