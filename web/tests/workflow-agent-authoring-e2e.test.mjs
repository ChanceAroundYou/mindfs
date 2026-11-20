import assert from "node:assert/strict";
import path from "node:path";
import { test } from "vitest";
import fs from "./source-map-fs.mjs";
import {
  initialWorkflowAuthoringState,
  workflowAuthoringReducer,
  adoptConflictRevision,
} from "../src/components/task/workflowAuthoring/proposalState.ts";
import {
  canApplyWorkflowProposal,
  defaultWorkflowSelection,
  resolveWorkflowSelection,
  workflowReviewIssues,
} from "../src/components/task/workflowAuthoring/proposalSelection.ts";
import { workflowPatchConflictRevision } from "../src/services/task/workflowDraft.ts";

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

const response = {
  summary: "Add a notify after the agent",
  rationale: "The run should announce completion",
  base_revision: "r1",
  operations: [
    { id: "op-node", op: "add_node", node: { id: "b", kind: "notify", params: { message: "done" } } },
    { id: "op-edge", op: "add_edge", edge: { from: "a", to: "b" } },
    { id: "op-remove", op: "remove_future_node", node_id: "old" },
  ],
};
const diff = { operations: [{ operation_id: "op-node", op: "add_node", node_id: "b" }] };
const target = { scope: "draft", resourceID: "wf", revision: "r1" };

function reviewed(extra = {}) {
  let state = workflowAuthoringReducer(initialWorkflowAuthoringState, { type: "begin", target });
  state = workflowAuthoringReducer(state, { type: "resolve", token: state.token, response: { ...response, ...extra } });
  return state;
}

test("natural language → proposal → preview stays unpersisted until an explicit apply", () => {
  let state = reviewed();
  assert.equal(state.phase, "review");
  assert.equal(state.proposal.status, "preview", "a proposal is never ready before server validation");
  assert.deepEqual(defaultWorkflowSelection(state.proposal), [], "nothing is accepted by default");
  assert.equal(canApplyWorkflowProposal(state.proposal, []), false);

  state = workflowAuthoringReducer(state, { type: "validation", diff, dryRun: { status: "done" } });
  assert.equal(state.proposal.status, "ready");
  assert.equal(canApplyWorkflowProposal(state.proposal, ["op-node", "op-edge"]), true);

  state = workflowAuthoringReducer(state, { type: "apply-start" });
  assert.equal(state.proposal.status, "applying");
  state = workflowAuthoringReducer(state, { type: "apply-success", revision: "r2" });
  assert.equal(state.proposal.status, "applied");
  assert.equal(state.target.revision, "r2", "reload/seeded state follows the applied revision");
});

test("partial acceptance drops dangling edges instead of persisting them", () => {
  const operations = response.operations;
  const nodeOnly = resolveWorkflowSelection(operations, ["op-node"]);
  assert.deepEqual(nodeOnly.operations.map((op) => op.id), ["op-node"]);
  assert.deepEqual(nodeOnly.dropped, [], "an unselected edge is simply not part of the subset");

  const edgeOnly = resolveWorkflowSelection(operations, ["op-edge"]);
  assert.deepEqual(edgeOnly.operations, [], "an edge whose new endpoint is unselected cannot be applied");
  assert.ok(edgeOnly.dropped.some((d) => d.id === "op-edge" && d.reason === "dangling-edge"));

  const full = resolveWorkflowSelection(operations, ["op-node", "op-edge", "op-remove"]);
  assert.deepEqual(full.operations.map((op) => op.id), ["op-node", "op-edge", "op-remove"]);
  assert.deepEqual(full.dropped, []);

  // Removing a node auto-includes the edge that touches it.
  const withEdge = [...operations, { id: "op-edge-old", op: "remove_future_edge", edge: { from: "a", to: "old" } }];
  const removed = resolveWorkflowSelection(withEdge, ["op-remove"]);
  assert.deepEqual(removed.operations.map((op) => op.id), ["op-remove", "op-edge-old"]);
  assert.deepEqual(removed.autoIncluded, ["op-edge-old"]);
});

test("stale, malformed, blocked and concurrent paths never leave an applicable proposal", () => {
  let state = reviewed();
  state = workflowAuthoringReducer(state, { type: "validation", diff, dryRun: { status: "done" } });
  const stale = workflowAuthoringReducer(state, {
    type: "observe-revision",
    target: { ...target, revision: "r2" },
  });
  assert.equal(stale.proposal.status, "stale");
  assert.equal(canApplyWorkflowProposal(stale.proposal, ["op-node"]), false);

  const malformed = reviewed({ summary: "", operations: [] });
  assert.equal(malformed.proposal.status, "invalid");
  assert.ok(malformed.proposal.issues.length > 0);

  const blocked = workflowAuthoringReducer(state, {
    type: "validation",
    diff,
    dryRun: { status: "waiting_user", issues: [{ field: "graph", message: "missing input" }] },
  });
  assert.equal(blocked.proposal.status, "invalid", "dry-run issues block even without a 422");
  assert.equal(canApplyWorkflowProposal(blocked.proposal, ["op-node"]), false);

  state = workflowAuthoringReducer(state, { type: "apply-start" });
  const conflict = workflowAuthoringReducer(state, {
    type: "apply-conflict",
    currentRevision: "r2",
    error: "draft conflict",
  });
  assert.equal(conflict.proposal.status, "conflict");
  assert.equal(conflict.proposal.operations.length, response.operations.length, "the proposal is kept, not discarded");
  assert.equal(conflict.target.revision, "r2", "the reviewer learns the current revision");
  assert.equal(canApplyWorkflowProposal(conflict.proposal, ["op-node"]), false);
});

test("the review hook applies once, revalidates the subset, and never publishes", () => {
  const hook = read("src/components/task/useWorkflowAuthoringReview.ts");
  assert.match(hook, /reviewWorkflowDraftPatch|reviewWorkflowRunPatch/, "subset is revalidated server-side");
  assert.match(hook, /applyWorkflowDraftPatch|applyWorkflowRunPatch/, "apply goes through the existing CAS endpoint");
  assert.match(hook, /busy/, "duplicate clicks are gated by a busy guard");
  assert.doesNotMatch(hook, /publishWorkflowDraft|ProposeWorkflowPublish/, "authoring never publishes");
  assert.doesNotMatch(hook, /\bretry\(|\brebase\(|setInterval/, "conflicts are never retried or rebased automatically");

  const generation = read("src/components/task/useWorkflowAuthoring.ts");
  assert.doesNotMatch(generation, /\bapplyWorkflow\w*\(|publishWorkflow\w*\(/, "generation cannot persist");
});

test("保存即发布：一次保存先写草稿，再用新修订静默发布，界面上没有发布按钮", () => {
  // 用户 2026-11-20 裁定「保存即发布，彻底清理掉发布流程，也不做版本的概念了」。
  // 执行侧仍只认已发布版本，所以 hook 内部必须补上 publish，但 UI 不暴露它。
  const hook = read("src/components/task/useWorkflowDraftEditor.ts");
  const save = hook.slice(hook.indexOf("const save = useCallback"), hook.indexOf("const revert = useCallback"));
  assert.match(save, /updateWorkflowDraft\(/, "先写草稿");
  assert.match(save, /publishWorkflowDraft\(updated\.workflow_id, \{ revision: updated\.revision \}\)/, "再用刚拿到的新修订发布，不能用旧修订");
  assert.match(save, /setDirty\(true\)/, "发布失败时保留脏状态，让「保存」还能重试");
  assert.doesNotMatch(hook, /confirmDialog\(\{ message: t\("task\.workflowEditor\.publishConfirm"/, "保存不再走二次确认");
  assert.doesNotMatch(hook, /\bpublish:/, "hook 不再对外暴露 publish");

  const editor = read("src/components/task/WorkflowDraftEditor.tsx");
  assert.doesNotMatch(editor, /e\.publish|workflow-draft-publish/, "编辑器里没有发布按钮");
  assert.match(editor, /data-testid="workflow-draft-save"/);
  assert.match(editor, /data-testid="workflow-draft-revert"/);

  const svc = read("src/services/task/workflowDraft.ts");
  assert.match(svc, /confirmed_revision/, "发布接口本身保留（执行侧仍按版本解析），只是 UI 不再让它成为一条流程");
});

test("mobile keeps editing: narrow screens no longer downgrade the canvas", () => {
  const canvas = read("src/components/task/WorkflowCanvas.tsx");
  assert.match(canvas, /if \(readOnly\)/, "only an explicit readOnly takes the read-only list");
  assert.doesNotMatch(canvas, /narrow \|\| readOnly/, "narrow screens must stay editable");
  assert.match(canvas, /workflow-canvas-readonly/);
  assert.match(canvas, /workflowNodeConfigText/, "the read-only list shows each node's prompt, not just its kind");
  assert.match(canvas, /loopInnerGraph/, "loop inner nodes are listed too, with their own prompts");
  const editor = read("src/components/task/WorkflowDraftEditor.tsx");
  assert.doesNotMatch(editor, /readOnly=\{isMobile\}/, "mobile edits the real canvas");
});

test("server dry-run issues are the apply gate, not a status string", () => {
  assert.deepEqual(workflowReviewIssues({ dry_run: { status: "waiting_user" } }), []);
  const issues = workflowReviewIssues({
    dry_run: { status: "waiting_user", issues: [{ field: "graph", message: "missing input" }] },
  });
  assert.equal(issues.length, 1, "a 200 response with dry-run issues must block apply");

  const hook = read("src/components/task/useWorkflowAuthoringReview.ts");
  assert.match(hook, /workflowReviewIssues\(review\)/, "apply consults the server dry-run issues");
  assert.match(hook, /if \(dryRunIssues\.length > 0\)[\s\S]{0,200}?return null/, "issues stop before the CAS write");
  assert.doesNotMatch(hook, /status === "blocked"/, "the backend never emits a blocked status");
  const selection = read("src/components/task/workflowAuthoring/proposalSelection.ts");
  assert.doesNotMatch(selection, /status === "blocked"/);
  const state = read("src/components/task/workflowAuthoring/proposalState.ts");
  assert.doesNotMatch(state, /status === "blocked"/);
});

test("CAS conflict payloads normalize Draft and Run revisions", () => {
  assert.equal(workflowPatchConflictRevision({ payload: { current_revision: "r2" } }), "r2");
  assert.equal(workflowPatchConflictRevision({ payload: { current_revision: 3 } }), "3");
  assert.equal(workflowPatchConflictRevision(new Error("other")), "");
});

test("a generation CAS conflict adopts the server revision and requests a reload", () => {
  const adopted = adoptConflictRevision(
    { scope: "draft", resourceID: "wf", revision: "r1" },
    { payload: { current_revision: "r2" } },
  );
  assert.equal(adopted.conflictRevision, "r2");
  assert.equal(adopted.target.revision, "r2", "the next attempt must not resend the stale base");

  const untouched = adoptConflictRevision(
    { scope: "draft", resourceID: "wf", revision: "r1" },
    new Error("boom"),
  );
  assert.equal(untouched.conflictRevision, "");
  assert.equal(untouched.target.revision, "r1");

  const hook = read("src/components/task/useWorkflowAuthoring.ts");
  assert.match(hook, /adoptConflictRevision\(target, error\)/);
  assert.match(hook, /observe-revision/);
  const editor = read("src/components/task/WorkflowDraftEditor.tsx");
  assert.match(editor, /conflictRevision[\s\S]{0,160}reloadSelected/);
  const state = read("src/components/task/useWorkflowDraftEditor.ts");
  assert.match(
    state,
    /const reloadSelected = useCallback\([\s\S]{0,500}?listWorkflowDrafts\(\)/,
    "a conflict reloads the control-plane draft instead of keeping the stale canvas",
  );
});

test("create scope is explicit and persists only through the create apply", () => {
  const editor = read("src/components/task/WorkflowDraftEditor.tsx");
  assert.match(editor, /sendTestId="workflow-agent-create"/, "create has its own entry point (the create dialog's PromptEditor send action)");
  assert.match(editor, /data-testid="workflow-create-open"/, "the sidebar exposes one create entry instead of duplicate header buttons");
  assert.doesNotMatch(editor, /t\("task\.workflowEditor\.create"\)/, "the duplicate manual New header button is gone");
  assert.match(editor, /scope: createMode \? "create" : "draft"/, "create generation never reuses the draft gateway");
  assert.match(editor, /revision: createMode \? "r0" : e\.revision/, "create reviews against the synthetic base");
  assert.match(editor, /setCreateMode\(false\)[\s\S]{0,80}adoptDraft/, "apply adopts r1 and leaves create mode");
  assert.match(editor, /createMode \? null : e\.selected/, "create mode does not edit an existing draft canvas");

  const review = read("src/components/task/useWorkflowAuthoringReview.ts");
  assert.match(review, /target\.scope === "create"[\s\S]{0,160}applyWorkflowCreateProposal/, "create apply is separate from CAS draft apply");
  assert.doesNotMatch(review, /create[\s\S]{0,80}publishWorkflowDraft/, "create never publishes");
});

test("Loop save/reopen keeps the inner DAG, and Run edits report backend conflicts", () => {
  const model = read("src/shared/workflowModel.ts");
  assert.match(model, /params\.graph|graph: \{ nodes/, "the inner graph is serialized under params.graph");
  const review = read("src/components/task/workflowAuthoring/WorkflowProposalReview.tsx");
  assert.match(review, /workflow-proposal-conflict/, "backend CAS conflicts are shown to the reviewer");
  assert.match(review, /workflow-proposal-stale/, "frozen/stale revisions are shown instead of retried");
});