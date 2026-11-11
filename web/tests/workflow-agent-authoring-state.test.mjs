import assert from "node:assert/strict";
import { test } from "vitest";
import {
  WORKFLOW_AUTHORING_MAX_OPERATIONS,
  initialWorkflowAuthoringState,
  workflowAuthoringReducer as reduce,
} from "../src/components/task/workflowAuthoring/proposalState.ts";

const draft = (revision) => ({ scope: "draft", resourceID: "wf-1", revision });
const run = (revision) => ({ scope: "run", resourceID: "run-9", revision });

const nodeOp = (id, nodeId = "n1") => ({
  id,
  op: "add_node",
  node: { id: nodeId, kind: "agent", params: { agent: "claude" } },
});

const answer = (baseRevision, operations = [nodeOp("op-1")]) => ({
  base_revision: baseRevision,
  summary: "Add an agent node",
  rationale: "The goal needs one agent step",
  operations,
});

const begin = (target) => reduce(initialWorkflowAuthoringState, { type: "begin", target });
const generate = (state, response, token = state.token) => reduce(state, { type: "resolve", token, response });

test("generation is bound to the snapshot revision that was read", () => {
  const state = begin(draft("1"));
  assert.equal(state.phase, "generating");
  assert.deepEqual(state.target, draft("1"));
  assert.equal(state.proposal, null);
});

test("a response that lands after the snapshot moved is stale and keeps the new revision", () => {
  let state = begin(draft("1"));
  const token = state.token;
  state = reduce(state, { type: "observe-revision", target: draft("2") });
  state = generate(state, answer("1"), token);

  assert.equal(state.target.revision, "2", "the newer snapshot must not be overwritten");
  assert.equal(state.proposal.status, "stale");
  assert.equal(state.proposal.baseRevision, "1");
  assert.equal(state.proposal.operations.length, 1, "stale proposals stay inspectable");
});

test("a late response after switching targets is ignored", () => {
  let state = begin(draft("1"));
  const staleToken = state.token;
  state = reduce(state, { type: "begin", target: run("7") });
  state = generate(state, answer("1"), staleToken);

  assert.equal(state.phase, "generating");
  assert.deepEqual(state.target, run("7"));
  assert.equal(state.proposal, null, "a response for another target cannot become the proposal");
});

test("a late response after cancel is ignored", () => {
  let state = begin(draft("1"));
  const token = state.token;
  state = reduce(state, { type: "cancel" });
  state = generate(state, answer("1"), token);

  assert.equal(state.phase, "idle");
  assert.equal(state.proposal, null);
  assert.equal(state.target, null);
});

test("a malformed response becomes invalid with a localizable issue", () => {
  let state = begin(draft("1"));
  state = generate(state, { base_revision: "1", operations: "not-an-array" });

  assert.equal(state.phase, "review");
  assert.equal(state.proposal.status, "invalid");
  assert.ok(state.proposal.issues.length >= 1);
  assert.match(state.proposal.issues.map((issue) => issue.message).join(" "), /operations/);
  assert.equal(state.proposal.operations.length, 0);
  assert.deepEqual(state.target, draft("1"));
});

test("duplicate operation ids are rejected", () => {
  let state = begin(draft("1"));
  state = generate(state, answer("1", [nodeOp("op-1", "n1"), nodeOp("op-1", "n2")]));

  assert.equal(state.proposal.status, "invalid");
  assert.match(state.proposal.issues.map((issue) => issue.message).join(" "), /duplicate/i);
});

test("more than the operation limit is rejected", () => {
  const operations = Array.from({ length: WORKFLOW_AUTHORING_MAX_OPERATIONS + 1 }, (_, index) =>
    nodeOp(`op-${index}`, `n-${index}`),
  );
  let state = begin(draft("1"));
  state = generate(state, answer("1", operations));

  assert.equal(state.proposal.status, "invalid");
  assert.match(state.proposal.issues[0].message, /200/);
});

test("unknown operations are rejected instead of being passed through", () => {
  let state = begin(draft("1"));
  state = generate(state, answer("1", [{ id: "op-1", op: "replace_graph", node: { id: "n1", kind: "agent" } }]));

  assert.equal(state.proposal.status, "invalid");
  assert.match(state.proposal.issues[0].message, /unknown operation/);
});

test("a generation rejection surfaces issues against the same target", () => {
  let state = begin(draft("1"));
  state = reduce(state, {
    type: "reject",
    token: state.token,
    issues: [{ field: "response", message: "response must contain a JSON object" }],
  });

  assert.equal(state.phase, "review");
  assert.equal(state.proposal.status, "invalid");
  assert.equal(state.proposal.baseRevision, "1");
  assert.equal(state.proposal.issues.length, 1);
  assert.deepEqual(state.target, draft("1"));
});

test("a valid response starts in preview and needs server validate and dry-run before ready", () => {
  let state = begin(draft("1"));
  state = generate(state, answer("1"));

  assert.equal(state.phase, "review");
  assert.equal(state.proposal.status, "preview");
  assert.equal(state.proposal.baseRevision, "1");
  assert.equal(state.proposal.operations[0].id, "op-1");

  state = reduce(state, {
    type: "validation",
    diff: { operations: [{ operation_id: "op-1", op: "add_node" }] },
    dryRun: { status: "done", steps: [] },
    issues: [],
  });
  assert.equal(state.proposal.status, "ready");
  assert.equal(state.proposal.validation.operations.length, 1);
  assert.equal(state.proposal.dryRun.status, "done");
});

test("validation issues keep the proposal invalid", () => {
  let state = begin(draft("1"));
  state = generate(state, answer("1"));
  state = reduce(state, {
    type: "validation",
    diff: { operations: [] },
    dryRun: { status: "waiting_user", steps: [] },
    issues: [{ operation_id: "op-1", field: "node.id", message: "duplicate node id" }],
  });

  assert.equal(state.proposal.status, "invalid");
  assert.equal(state.proposal.issues.length, 1);
});

test("a dry-run issue alone invalidates the proposal", () => {
  let state = begin(draft("1"));
  state = generate(state, answer("1"));
  state = reduce(state, {
    type: "validation",
    diff: { operations: [] },
    dryRun: { status: "waiting_user", steps: [], issues: [{ field: "graph", message: "condition input missing" }] },
    issues: [],
  });

  assert.equal(state.proposal.status, "invalid", "the server dry-run issue is the blocking signal");
  assert.equal(state.proposal.issues.length, 1);
});

test("a snapshot change after preview marks the proposal stale without discarding it", () => {
  let state = begin(draft("1"));
  state = generate(state, answer("1"));
  state = reduce(state, { type: "validation", diff: { operations: [] }, dryRun: { status: "done" }, issues: [] });
  state = reduce(state, { type: "observe-revision", target: draft("2") });

  assert.equal(state.phase, "review");
  assert.equal(state.proposal.status, "stale");
  assert.equal(state.proposal.baseRevision, "1");
  assert.equal(state.target.revision, "2");
});

test("a CAS conflict keeps the proposal and never retries or rebases", () => {
  let state = begin(draft("1"));
  state = generate(state, answer("1"));
  state = reduce(state, { type: "validation", diff: { operations: [] }, dryRun: { status: "done" }, issues: [] });
  state = reduce(state, { type: "apply-start" });
  assert.equal(state.proposal.status, "applying");

  state = reduce(state, { type: "apply-conflict", currentRevision: "2" });
  assert.equal(state.proposal.status, "conflict");
  assert.equal(state.proposal.baseRevision, "1", "conflict must not rebase the proposal");
  assert.equal(state.target.revision, "2");
});
const create = () => ({ scope: "create", resourceID: "new-wf", revision: "r0" });

test("create scope binds the synthetic r0 base and stays reviewable", () => {
  const state = generate(begin(create()), answer("r0"));
  assert.equal(state.phase, "review");
  assert.equal(state.proposal.status, "preview");
  assert.equal(state.proposal.baseRevision, "r0");
  assert.deepEqual(state.target, create());
});

test("a create response for another base is stale instead of overwriting the target", () => {
  const state = generate(begin(create()), answer("r1"));
  assert.equal(state.target.revision, "r0");
  assert.equal(state.proposal.status, "stale");
});
