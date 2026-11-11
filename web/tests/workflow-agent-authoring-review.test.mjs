import assert from "node:assert/strict";
import path from "node:path";
import { test } from "vitest";
import fs from "./source-map-fs.mjs";
import {
  canApplyWorkflowProposal,
  defaultWorkflowSelection,
  resolveWorkflowSelection,
} from "../src/components/task/workflowAuthoring/proposalSelection.ts";
import { nodeChangeSides } from "../src/components/task/workflowAuthoring/proposalDiff.ts";

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

const addNode = (id, nodeId) => ({ id, op: "add_node", node: { id: nodeId, kind: "agent", params: { agent: "claude" } } });
const addEdge = (id, from, to) => ({ id, op: "add_edge", edge: { from, to } });
const removeNode = (id, nodeId) => ({ id, op: "remove_future_node", node_id: nodeId });
const removeEdge = (id, from, to) => ({ id, op: "remove_future_edge", edge: { from, to } });
const ids = (result) => result.operations.map((op) => op.id);

test("nothing is accepted before the user reviews the proposal", () => {
  const proposal = { operations: [addNode("op-1", "a"), addNode("op-2", "b")] };
  assert.deepEqual(defaultWorkflowSelection(proposal), []);
});

test("a partial selection keeps the proposal order and ignores unknown ids", () => {
  const operations = [addNode("op-1", "a"), addNode("op-2", "b"), addNode("op-3", "c")];
  const result = resolveWorkflowSelection(operations, ["op-3", "op-1", "missing"]);
  assert.deepEqual(ids(result), ["op-1", "op-3"]);
  assert.deepEqual(result.dropped, []);
});

test("accepting an edge whose added endpoint was rejected drops the edge instead of dangling", () => {
  const operations = [addNode("op-1", "b"), addEdge("op-2", "a", "b")];
  const result = resolveWorkflowSelection(operations, ["op-2"]);
  assert.deepEqual(ids(result), []);
  assert.deepEqual(result.dropped, [{ id: "op-2", reason: "dangling-edge" }]);
});

test("a rejected add_node does not take its edges with it when the edge has no added endpoint", () => {
  const operations = [addNode("op-1", "b"), addEdge("op-2", "a", "existing")];
  const result = resolveWorkflowSelection(operations, ["op-2"]);
  assert.deepEqual(ids(result), ["op-2"]);
});

test("removing a node auto-includes the proposal's edge removals touching it", () => {
  const operations = [
    addEdge("op-0", "x", "y"),
    removeEdge("op-1", "start", "old"),
    removeNode("op-2", "old"),
    removeEdge("op-3", "old", "end"),
    removeNode("op-4", "other"),
  ];
  const result = resolveWorkflowSelection(operations, ["op-2"]);
  assert.deepEqual(ids(result), ["op-1", "op-2", "op-3"]);
  assert.deepEqual(result.autoIncluded, ["op-1", "op-3"]);
});

test("an edge into a node removed by the same selection is dropped", () => {
  const operations = [addEdge("op-1", "start", "old"), removeNode("op-2", "old")];
  const result = resolveWorkflowSelection(operations, ["op-1", "op-2"]);
  assert.deepEqual(ids(result), ["op-2"]);
  assert.deepEqual(result.dropped, [{ id: "op-1", reason: "edge-touches-removed-node" }]);
});

test("an empty selection cannot be applied", () => {
  const proposal = { status: "ready", baseRevision: "r1", operations: [addNode("op-1", "a")], dryRun: { status: "done" } };
  assert.equal(canApplyWorkflowProposal(proposal, []), false);
  assert.equal(canApplyWorkflowProposal(proposal, ["op-1"]), true);
});

test("invalid, stale, issue-blocked, applying and conflicted proposals cannot be applied", () => {
  const operations = [addNode("op-1", "a")];
  const selected = ["op-1"];
  for (const status of ["preview", "invalid", "stale", "applying", "applied", "conflict", "error"]) {
    const proposal = { status, baseRevision: "r1", operations, dryRun: { status: "done" } };
    assert.equal(canApplyWorkflowProposal(proposal, selected), false, status);
  }
  // The backend's dry-run status is only done/waiting_user; it reports blocking
  // through `issues`, so that is what the gate must read.
  const blocked = {
    status: "ready",
    baseRevision: "r1",
    operations,
    dryRun: { status: "waiting_user", issues: [{ operation_id: "op-1", field: "node", message: "condition input missing" }] },
  };
  assert.equal(canApplyWorkflowProposal(blocked, selected), false);
});

test("selection survives a proposal that has no operations", () => {
  const result = resolveWorkflowSelection([], ["op-1"]);
  assert.deepEqual(result.operations, []);
  assert.equal(canApplyWorkflowProposal({ status: "ready", baseRevision: "r1", operations: [] }, ["op-1"]), false);
});

test("the review UI starts with nothing checked and shows every change class", () => {
  const diff = read("src/components/task/workflowAuthoring/WorkflowProposalDiff.tsx");
  assert.match(diff, /checked=\{[^}]*selected/, "checkbox state comes from the reviewed selection");
  assert.match(diff, /data-testid="workflow-proposal-op"/, "each operation is individually addressable");
  assert.match(diff, /params/, "parameter changes are shown");
  assert.match(diff, /position/, "layout changes are shown");
  assert.match(diff, /data-field="before"/, "updates show the value before the change");
  assert.match(diff, /data-field="after"/, "updates show the value after the change");
  assert.match(diff, /edge/i, "edge changes are shown");
  assert.doesNotMatch(diff, /applyWorkflowDraftPatch\(|validateWorkflowDraftPatch\(/, "the diff view is presentation only");
});

test("the review UI disables apply unless the server made the subset ready", () => {
  const review = read("src/components/task/workflowAuthoring/WorkflowProposalReview.tsx");
  assert.match(review, /data-testid="workflow-proposal-review"/, "review panel is addressable");
  assert.match(review, /data-testid="workflow-proposal-apply"/, "apply button is addressable");
  assert.match(review, /disabled=\{[^}]*canApply/, "apply is gated by the review state");
  assert.match(review, /WorkflowProposalDiff/, "review renders the per-operation diff");
  assert.match(review, /WorkflowProposalSummary/, "review renders the summary and rationale");
  assert.match(review, /data-testid="workflow-proposal-stale"/, "stale proposals are explained instead of auto-rebased");
  assert.match(review, /data-testid="workflow-proposal-conflict"/, "CAS conflicts are explained instead of retried");
});

test("the review hook revalidates the subset and applies exactly once", () => {
  const generation = read("src/components/task/useWorkflowAuthoring.ts");
  const review = read("src/components/task/useWorkflowAuthoringReview.ts");
  assert.match(review, /resolveWorkflowSelection\(/, "subset comes from the pure selection rules");
  assert.match(review, /reviewWorkflowDraftPatch\(/, "Draft subsets are revalidated on the server");
  assert.match(review, /reviewWorkflowRunPatch\(/, "Run subsets are revalidated on the server");
  assert.match(review, /applyWorkflowDraftPatch\(/, "Draft apply uses the existing typed CAS endpoint");
  assert.match(review, /applyWorkflowRunPatch\(/, "Run apply uses the existing typed CAS endpoint");
  assert.match(review, /busyRef\.current/, "duplicate clicks cannot start a second apply");
  assert.doesNotMatch(review, /\bretry\(|\brebase\(|setInterval/, "conflicts are never retried automatically");
  assert.match(review, /status !== "ready"/, "only a server-ready proposal can start applying");
  assert.match(generation, /type: "apply-conflict"/, "CAS conflicts are surfaced without rebasing");
});

test("the patch services expose subset review with server dry-run", () => {
  const service = read("src/services/task/workflowDraft.ts");
  for (const name of ["reviewWorkflowDraftPatch", "reviewWorkflowRunPatch", "applyWorkflowRunPatch"]) {
    assert.match(service, new RegExp(`export async function ${name}\\(`), `${name} is exported`);
  }
  const reviewDraft = service.match(/export async function reviewWorkflowDraftPatch[\s\S]*?\n}/)?.[0] || "";
  assert.match(reviewDraft, /patch\/validate/, "review reuses the server validate endpoint");
  assert.match(service, /export type WorkflowPatchReview = \{[\s\S]*?dry_run\?: WorkflowDryRunPayload;/, "review carries the server dry-run result");
  const runApply = service.match(/export async function applyWorkflowRunPatch[\s\S]*?\n}/)?.[0] || "";
  assert.match(runApply, /workflow-run\/patch\/apply/, "Run apply reuses the existing CAS endpoint");
});

test("the draft editor wires generation and review without a second graph truth", () => {
  const editor = read("src/components/task/WorkflowDraftEditor.tsx");
  assert.match(editor, /useWorkflowAuthoring\(/, "the editor owns the authoring state machine");
  assert.match(editor, /WorkflowProposalReview/, "the editor renders the review panel");
  assert.match(editor, /observeRevision\(/, "editor revision changes mark the proposal stale");
  assert.match(editor, /onApplied/, "apply returns the authoritative draft to the editor");
});
test("update operations show both the before and after node state", () => {
  const sides = nodeChangeSides(
    { id: "a", kind: "agent", params: { model: "old-model" } },
    { id: "a", kind: "agent", params: { model: "new-model" } },
  );
  assert.match(sides.before, /old-model/);
  assert.match(sides.after, /new-model/);
  const added = nodeChangeSides(undefined, { id: "b", kind: "notify", params: { message: "done" } });
  assert.equal(added.before, "");
  assert.match(added.after, /done/);
});
