import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import { LOOP_DEFAULT, LOOP_MAX, LOOP_MIN, newNode, validateWorkflowGraph } from "../src/shared/workflowModel.ts";

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
      { id: "l1", kind: "loop", params: { max_iterations: LOOP_DEFAULT } },
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

test("画布只在桌面渲染、移动端只读、脏标记与离开提醒存在", () => {
  const canvas = read("../src/components/task/WorkflowCanvas.tsx");
  // 移动端断点 + 只读分支。
  assert.match(canvas, /MOBILE_BREAKPOINT = 720/);
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
