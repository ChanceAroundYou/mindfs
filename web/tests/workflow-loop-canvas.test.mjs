import assert from "node:assert/strict";
import path from "node:path";
import { test } from "vitest";
import fs from "./source-map-fs.mjs";
import {
  LOOP_DEFAULT,
  loopInnerGraph,
  newNode,
  validateWorkflowGraph,
  withLoopInnerGraph,
} from "../src/shared/workflowModel.ts";
import { flowEdges, flowNodes, graphFrom } from "../src/components/task/workflowAuthoring/flowGraph.ts";

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

const inner = {
  nodes: [
    { id: "step", kind: "agent", name: "step", params: { agent: "claude", prompt: "work" }, position: { x: 10, y: 20 } },
    { id: "check", kind: "condition", params: { expression: "step.output.ok", routes: ["pass", "fail"] }, position: { x: 240, y: 20 } },
  ],
  edges: [{ from: "step", to: "check" }],
};

const loopNode = (overrides = {}) => ({
  id: "loop-1",
  kind: "loop",
  name: "retry",
  params: { max_iterations: 3, exit_expression: "check.output.pass", graph: inner },
  ...overrides,
});

test("a Loop inner graph round-trips through the persisted params.graph key", () => {
  const node = loopNode();
  const restored = JSON.parse(JSON.stringify(node));
  assert.deepEqual(loopInnerGraph(restored), inner);
  assert.equal(restored.params.max_iterations, 3);
  assert.equal(restored.params.exit_expression, "check.output.pass");
  const rebuilt = withLoopInnerGraph(node, { nodes: [...inner.nodes], edges: [] });
  assert.deepEqual(loopInnerGraph(rebuilt).edges, []);
  assert.equal(rebuilt.params.max_iterations, 3, "editing the inner graph never drops loop config");
  assert.deepEqual(loopInnerGraph(rebuilt).nodes[0].position, { x: 10, y: 20 }, "positions survive the round trip");
});

test("dragging an inner node only moves layout, never its business params", () => {
  const moved = flowNodes(inner, { agent: "Agent", condition: "Condition" }).map((n) =>
    n.id === "step" ? { ...n, position: { x: 99, y: 100 } } : n,
  );
  const next = graphFrom(moved, flowEdges(inner), inner);
  const step = next.nodes.find((n) => n.id === "step");
  assert.deepEqual(step.params, inner.nodes[0].params);
  assert.equal(step.kind, "agent");
  assert.deepEqual(step.position, { x: 99, y: 100 });
});

test("a new Loop starts with a bound, an empty exit expression and an inner graph", () => {
  const node = newNode("loop");
  assert.equal(node.params.max_iterations, LOOP_DEFAULT);
  assert.equal(node.params.exit_expression, "");
  assert.ok(loopInnerGraph(node), "the sub-canvas has somewhere to attach nodes");
});

test("a valid Loop with an inner DAG passes validation", () => {
  const errors = validateWorkflowGraph({ nodes: [loopNode(), { id: "after", kind: "notify", params: {} }], edges: [{ from: "loop-1", to: "after" }] });
  assert.deepEqual(errors, []);
});

test("Loop bounds and exit expression are mandatory", () => {
  for (const max of [0, 21]) {
    const errors = validateWorkflowGraph({ nodes: [loopNode({ params: { ...loopNode().params, max_iterations: max } })] });
    assert.ok(errors.some((e) => e.includes("max_iterations")), `max=${max}`);
  }
  const noExit = validateWorkflowGraph({ nodes: [loopNode({ params: { ...loopNode().params, exit_expression: "  " } })] });
  assert.ok(noExit.some((e) => e.includes("exit_expression")), noExit.join("|"));
});

test("a Loop without an inner graph is rejected", () => {
  const errors = validateWorkflowGraph({ nodes: [loopNode({ params: { max_iterations: 2, exit_expression: "x" } })] });
  assert.ok(errors.some((e) => e.includes("内部图")), errors.join("|"));
});

test("nested loops and inner human gates are rejected with explicit messages", () => {
  const nested = validateWorkflowGraph({ nodes: [loopNode({
    params: { ...loopNode().params, graph: { nodes: [{ id: "inner-loop", kind: "loop", params: { max_iterations: 2, exit_expression: "x", graph: inner } }], edges: [] } },
  })] });
  assert.ok(nested.some((e) => e.includes("嵌套")), nested.join("|"));

  const gate = validateWorkflowGraph({ nodes: [loopNode({
    params: { ...loopNode().params, graph: { nodes: [{ id: "gate", kind: "human_gate", params: {} }], edges: [] } },
  })] });
  assert.ok(gate.some((e) => e.includes("human_gate")), gate.join("|"));
});

test("inner ids cannot collide with top-level ids and inner edges cannot cross containers", () => {
  const collision = validateWorkflowGraph({
    nodes: [loopNode(), { id: "step", kind: "agent", params: {} }],
  });
  assert.ok(collision.some((e) => e.includes("冲突") || e.includes("重复")), collision.join("|"));

  const crossing = validateWorkflowGraph({ nodes: [loopNode({
    params: { ...loopNode().params, graph: { nodes: [...inner.nodes], edges: [{ from: "step", to: "outside" }] } },
  }), { id: "outside", kind: "agent", params: {} }] });
  assert.ok(crossing.some((e) => e.includes("outside")), crossing.join("|"));
});

test("the Loop sub-canvas is a nested editor with a breadcrumb and no second graph truth", () => {
  const loop = read("src/components/task/workflowAuthoring/WorkflowLoopCanvas.tsx");
  assert.match(loop, /ReactFlow/, "the sub-canvas reuses the existing ReactFlow renderer");
  assert.match(loop, /data-testid="workflow-loop-breadcrumb"/, "the breadcrumb back to the top graph exists");
  assert.match(loop, /withLoopInnerGraph\(/, "edits go through the shared Loop serializer");
  assert.match(loop, /data-testid="workflow-loop-add"/, "inner nodes can be added");
  assert.doesNotMatch(loop, /workflowDraft|protectedJSON|\bfetch\s*\(/, "the sub-canvas never persists");
  assert.doesNotMatch(loop, /kind === "loop"|"human_gate"/, "nested Loop and HumanGate cannot be added inside a Loop");
});

test("the top canvas enters the Loop inspector and edits exit expression and bound", () => {
  const canvas = read("src/components/task/WorkflowCanvas.tsx");
  // 节点属性浮动窗口已拆到 WorkflowNodeInspector（守 500 行硬线），Loop 的
  // 进入按钮与退出条件输入都在那边。
  const inspector = read("src/components/task/WorkflowNodeInspector.tsx");
  assert.match(canvas, /WorkflowLoopCanvas/, "the top canvas hosts the sub-canvas");
  assert.match(inspector, /data-testid="workflow-loop-enter"/, "a Loop can be opened explicitly");
  assert.match(canvas, /onNodeDoubleClick/, "double click also enters the Loop");
  assert.match(inspector, /data-testid="workflow-loop-exit"/, "exit expression is editable");
  assert.match(inspector, /exit_expression/, "exit expression is written to Loop params");
  assert.match(inspector, /max_iterations/, "iteration bound stays editable");
});