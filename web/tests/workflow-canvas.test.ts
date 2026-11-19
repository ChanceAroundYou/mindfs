import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import { LOOP_DEFAULT, LOOP_MAX, LOOP_MIN, NODE_KINDS, newNode, validateWorkflowGraph, workflowDraftDisplayName, workflowNodeConfigText } from "../src/shared/workflowModel.ts";
import { flowEdges, flowNodes } from "../src/components/task/workflowAuthoring/flowGraph.ts";
import { generateReactFlowNodeStyle } from "../src/components/task/workflowNodeStyles.ts";

const read = (p: string) => readFileSync(new URL(p, import.meta.url), "utf8");

test("画布校验与服务端语义一致：五类节点、Loop 上限、condition 必须有分支", () => {
  // 空图：至少一个节点。
  assert.ok(validateWorkflowGraph({ nodes: [] }).length > 0);
  // Loop 上限越界被拒（后端是 1..20，默认 5）。
  const bad = validateWorkflowGraph({
    nodes: [{ id: "l1", kind: "loop", params: { max_iterations: LOOP_MAX + 1 } }],
  });
  assert.ok(bad.some((e) => e.includes("max_iterations")), `expected loop bound error, got ${bad.join("|")}`);
  const ok = validateWorkflowGraph({
    nodes: [
      // 后端要求 Loop 同时具备 exit_expression 与内部图，fixture 必须按真实契约写。
      {
        id: "l1",
        kind: "loop",
        params: {
          max_iterations: LOOP_DEFAULT,
          exit_expression: "inner.output.ok",
          graph: { nodes: [{ id: "inner", kind: "agent", params: {} }], edges: [] },
        },
      },
      { id: "a1", kind: "agent", params: { writes_workspace: true } },
    ],
    edges: [{ from: "l1", to: "a1" }],
  });
  assert.deepEqual(ok, []);
  // condition 必须有 routes。
  const noRoutes = validateWorkflowGraph({ nodes: [{ id: "c1", kind: "condition", params: {} }] });
  assert.ok(noRoutes.some((e) => e.includes("route")));
  // 悬空边被拒。
  const dangling = validateWorkflowGraph({ nodes: [{ id: "a1", kind: "agent" }], edges: [{ from: "a1", to: "ghost" }] });
  assert.ok(dangling.some((e) => e.includes("ghost")));
  // 默认参数：agent 默认 writes_workspace=true（写节点默认串行）。
  assert.equal(newNode("agent").params?.writes_workspace, true);
  assert.equal(newNode("loop").params?.max_iterations, LOOP_DEFAULT);
  assert.ok(LOOP_MIN === 1 && LOOP_MAX === 20);
});

test("末端 human_gate 被画布校验拒绝：审批只能拦在执行中间", () => {
  // 与服务端 ValidateWorkflowDefinitionForAuthoring 同构（G-MY）。
  const terminal = validateWorkflowGraph({
    nodes: [
      { id: "a1", kind: "agent", params: {} },
      { id: "gate", kind: "human_gate", params: { prompt: "ok?" } },
    ],
    edges: [{ from: "a1", to: "gate" }],
  });
  assert.ok(
    terminal.some((e) => e.includes("human_gate")),
    `expected terminal gate error, got ${terminal.join("|")}`,
  );
  // 后面还有节点时正常。
  const withDownstream = validateWorkflowGraph({
    nodes: [
      { id: "a1", kind: "agent", params: {} },
      { id: "gate", kind: "human_gate", params: { prompt: "ok?" } },
      { id: "a2", kind: "agent", params: {} },
    ],
    edges: [
      { from: "a1", to: "gate" },
      { from: "gate", to: "a2" },
    ],
  });
  assert.deepEqual(withDownstream, []);
});

test("画布在窄屏同样是可编辑的（只读只是显式开关），脏标记与离开提醒存在", () => {
  const canvas = read("../src/components/task/WorkflowCanvas.tsx");
  // 窄屏断点仍在，但只影响排布（底部抽屉、图标按钮），不再把画布降级成只读清单。
  assert.match(canvas, /MOBILE_BREAKPOINT = 720/);
  // 只读清单只由显式 readOnly 触发：`narrow || readOnly` 曾让手机完全不能编辑。
  assert.match(canvas, /if \(readOnly\)/);
  assert.doesNotMatch(canvas, /narrow \|\| readOnly/);
  assert.match(canvas, /data-testid="workflow-canvas-readonly"/);
  // 脏红点与 beforeunload 离开提醒。
  assert.match(canvas, /data-testid="workflow-canvas-dirty"/);
  assert.match(canvas, /addEventListener\("beforeunload"/);
  // 校验错误可视化。
  assert.match(canvas, /data-testid="workflow-canvas-errors"/);
  // 无副作用：本组件不直接 fetch（落库由父层服务层负责）。
  assert.doesNotMatch(canvas, /\bfetch\s*\(/);
  assert.doesNotMatch(canvas, /protectedJSON/);
});

test("草稿服务层字段与后端契约一致（base_revision / confirmed_revision）", () => {
  const svc = read("../src/services/task/workflowDraft.ts");
  assert.ok(svc.includes("base_revision"), "update 必须带 base_revision 做 CAS");
  assert.ok(svc.includes("confirmed_revision"), "publish 必须确认具体 revision");
  assert.ok(svc.includes("/api/workflows/"), "走控制面 workflow 路由");
});

test("只读清单展示每个节点的 prompt，而不是只显示类型和 id", () => {
  assert.equal(workflowNodeConfigText({ id: "a", kind: "agent", params: { prompt: "先复现再修" } }), "先复现再修");
  assert.equal(workflowNodeConfigText({ id: "g", kind: "human_gate", params: { prompt: "确认发布？" } }), "确认发布？");
  assert.equal(workflowNodeConfigText({ id: "n", kind: "notify", params: { message: "完成" } }), "完成");
  assert.equal(workflowNodeConfigText({ id: "c", kind: "condition", params: { routes: ["pass", "fail"] } }), "pass / fail");
  assert.equal(
    workflowNodeConfigText({ id: "c", kind: "condition", params: { expression: "check.output.pass == true", routes: ["pass", "fail"] } }),
    "check.output.pass == true · pass / fail",
  );
  assert.match(workflowNodeConfigText({ id: "l", kind: "loop", params: { max_iterations: 3, exit_expression: "done" } }), /3 轮.*done/);
  assert.equal(workflowNodeConfigText({ id: "a", kind: "agent", params: {} }), "");
});

test("工作流标题优先显示元数据名称，缺省才回退内部 id", () => {
  assert.equal(workflowDraftDisplayName({ workflow_id: "workflow-debug", name: "Bug 修复" }), "Bug 修复");
  assert.equal(workflowDraftDisplayName({ workflow_id: "workflow-debug", name: "  " }), "debug");
  assert.equal(workflowDraftDisplayName({ workflow_id: "workflow-new-feature" }), "new-feature");
});

test("节点样式不得自带 transform：xyflow 就是靠它定位的", () => {
  // xyflow v12 的 NodeWrapper 把 `...node.style` 铺在 `transform: translate(x,y)`
  // **之后**（node_modules/@xyflow/react/dist/esm/index.js 的 NodeWrapper），
  // 所以 node.style 里出现任何 transform 都会顶掉定位：所有节点叠在画布原点，
  // 拖动只改 state、不改视觉 —— 这正是「节点看不见、拖不动」的现场。
  const graph = {
    nodes: NODE_KINDS.map((kind, i) => ({ id: `n${i}`, kind, params: {}, position: { x: 100 + i * 200, y: 120 } })),
    edges: [],
  };
  const nodes = flowNodes(graph, {});
  assert.equal(nodes.length, NODE_KINDS.length);
  for (const n of nodes) {
    assert.equal(n.style?.transform, undefined, `${n.id} 的 style 不得覆盖定位 transform`);
  }
  for (const kind of NODE_KINDS) {
    assert.equal(
      (generateReactFlowNodeStyle(kind) as Record<string, unknown>).transform,
      undefined,
      `${kind} 的节点样式不得覆盖定位 transform`,
    );
  }
  // 位置原样交给 flow 层：拖动后的坐标要能落库。
  assert.deepEqual(nodes.map((n) => n.position), graph.nodes.map((n) => n.position));
  assert.equal(flowEdges({ nodes: [], edges: [{ from: "n0", to: "n2" }] }).length, 1);
});

test("桌面节点属性面板能编辑 Agent / 人工确认 / 通知的文案", () => {
  const inspector = read("../src/components/task/WorkflowNodeInspector.tsx");
  assert.match(inspector, /selected\.params\?\.prompt/);
  assert.match(inspector, /selected\.kind === "human_gate"[\s\S]{0,500}?data-testid="workflow-gate-prompt"/);
  assert.match(inspector, /selected\.kind === "notify"[\s\S]{0,500}?data-testid="workflow-notify-message"/);
  assert.match(inspector, /data-testid="workflow-condition-expression"/);
  assert.match(inspector, /selected\.params\?\.message/);
});
