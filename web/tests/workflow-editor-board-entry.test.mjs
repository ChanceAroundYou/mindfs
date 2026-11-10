import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

test("workflow-editor-board-entry", () => {
  const root = path.resolve(import.meta.dirname, "..");
  const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");
  const workflowService = read("src/services/task/workflowDraft.ts");
  assert.match(workflowService, /listWorkflowDrafts\([\s\S]{0,250}?controlPath\(/, "Draft listing must use control-plane routing");
  assert.match(workflowService, /updateWorkflowDraft\([\s\S]{0,350}?controlPath\(/, "Draft save must use control-plane routing");
  assert.match(workflowService, /publishWorkflowDraft\([\s\S]{0,350}?controlPath\(/, "publishing must use control-plane routing");

  const toolbar = read("src/components/task/TaskBoardToolbar.tsx");
  const board = read("src/components/task/TaskBoardView.tsx");
  const owner = read("src/app/useAppKanbanTaskPanel.tsx");
  assert.match(toolbar, /onOpenWorkflowEditor/, "board toolbar exposes a workflow editor action");
  assert.match(board, /onOpenWorkflowEditor/, "TaskBoardView forwards editor navigation callback");
  assert.match(owner, /WorkflowDraftEditor/, "app-layer board owner mounts the independent editor view");

  const editor = read("src/components/task/WorkflowDraftEditor.tsx");
  const editorState = read("src/components/task/useWorkflowDraftEditor.ts");
  assert.match(editorState, /listWorkflowDrafts\(\)/, "editor loads shared control-plane drafts");
  assert.match(editorState, /createWorkflowDraft\(/, "editor can create a new Draft");
  assert.match(editorState, /updateWorkflowDraft\(/, "editor saves with Draft CAS");
  assert.match(editorState, /publishWorkflowDraft\(/, "editor publishes only after explicit revision confirmation");
  assert.match(editorState, /beforeunload/, "dirty Draft warns on page exit");
  assert.match(editor, /WorkflowCanvas/, "editor mounts visual canvas");
  assert.match(editorState, /confirmDialog/, "leaving editor uses the in-app dialog, not window.confirm");
  assert.doesNotMatch(editor, /window\.(confirm|alert|prompt)\s*\(/, "no system dialogs in the editor view");
  assert.match(editorState, /baseRevision/, "Draft save carries base_revision CAS");
  assert.match(editorState, /revision/, "publish carries confirmed_revision");
});
