import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import {
  setWorkflowNodeUsesOwnWorktree,
  workflowNodeUsesOwnWorktree,
} from "../src/shared/workflowModel.ts";
import {
  INSPECTOR_MAX_WIDTH,
  nodeScreenRect,
  placeInspectorNear,
} from "../src/components/task/workflowInspectorPlacement.ts";

const read = (p: string) => readFileSync(new URL(p, import.meta.url), "utf8");

// ── 独立 worktree 参数（后端 D10 只认 worktree=independent，别的值回落共享主树）──

test("独立 worktree：只认 independent，勾选写值、取消删键", () => {
  assert.equal(workflowNodeUsesOwnWorktree({ params: { worktree: "independent" } }), true);
  assert.equal(workflowNodeUsesOwnWorktree({ params: { worktree: " independent " } }), true);
  // 拼错/别的值后端一律回落共享主树，所以 UI 也不能显示成勾选态。
  assert.equal(workflowNodeUsesOwnWorktree({ params: { worktree: "independentt" } }), false);
  assert.equal(workflowNodeUsesOwnWorktree({ params: {} }), false);
  assert.equal(workflowNodeUsesOwnWorktree({}), false);

  const on = setWorkflowNodeUsesOwnWorktree({ prompt: "x" }, true);
  assert.equal(on.worktree, "independent");
  assert.equal(on.prompt, "x", "不能顺手丢掉别的参数");
  const off = setWorkflowNodeUsesOwnWorktree(on, false);
  assert.equal("worktree" in off, false);
  assert.equal(off.prompt, "x");
});

// ── 卡片定位：贴着节点框，右侧不够翻左侧，两侧都不够贴右边 ──

test("卡片定位：默认贴在节点右侧同高处", () => {
  const pos = placeInspectorNear({ left: 100, top: 200, right: 300 }, { width: 1400, height: 900 });
  assert.deepEqual(pos, { x: 310, y: 200 });
});

test("卡片定位：右侧放不下翻到左侧，左放不下就贴视口右边", () => {
  const flipped = placeInspectorNear({ left: 900, top: 100, right: 1100 }, { width: 1400, height: 900 });
  assert.equal(flipped.x, 900 - INSPECTOR_MAX_WIDTH - 10);
  const pinRight = placeInspectorNear({ left: 40, top: 100, right: 60 }, { width: 500, height: 900 });
  assert.ok(pinRight.x >= 10 && pinRight.x + INSPECTOR_MAX_WIDTH <= 500, `x=${pinRight.x} 必须留在视口内`);
});

test("卡片定位：底部放不下往上收，永远不出视口下沿", () => {
  const pos = placeInspectorNear({ left: 100, top: 700, right: 300 }, { width: 1400, height: 800 });
  assert.ok(pos.y + 500 <= 800, `y=${pos.y} 必须让预估高度留在视口内`);
  assert.ok(pos.y >= 10);
});

test("按 id 量节点框：优先整框而不是被点中的内层元素", () => {
  const rect = { left: 1, top: 2, right: 3 };
  let asked = "";
  const doc = {
    querySelector(selector: string) {
      asked = selector;
      return { getBoundingClientRect: () => rect };
    },
  };
  assert.equal(nodeScreenRect(doc, "n-abc"), rect);
  assert.equal(asked, '.react-flow__node[data-id="n-abc"]');
  // id 里的引号必须先转义，否则属性选择器直接抛错。
  nodeScreenRect(doc, 'we"ird');
  assert.equal(asked, '.react-flow__node[data-id="we\\"ird"]');
  assert.equal(nodeScreenRect(doc, ""), null);
});

// ── 源码不变量：这几条钉住「按了没反应」与「卡片留在原地」两个回归 ──

test("点卡片外面只关卡片，不能清选中（否则删除按钮 mousedown 后即 disabled，click 不再派发）", () => {
  const src = read("../src/components/task/WorkflowCanvas.tsx");
  const start = src.indexOf("const handleClickOutside");
  const end = src.indexOf('document.addEventListener("mousedown"', start);
  assert.ok(start > 0 && end > start, "找不到 handleClickOutside 主体");
  const body = src.slice(start, end);
  assert.ok(body.includes("setInspectorPosition(null)"), "点外部必须关掉卡片");
  assert.ok(!body.includes("closeInspector()"), "点外部不能顺手清掉选中 —— 那正是删除键失效的原因");
  assert.ok(!body.includes("setSelectedId"), "同理不能直接清 selectedId");
});

test("Delete 打死在输入控件里不生效，避免编 prompt 时删字顺手删节点", () => {
  const src = read("../src/components/task/WorkflowCanvas.tsx");
  assert.ok(src.includes("isContentEditable"), "缺 contentEditable 判据");
  assert.ok(src.includes('["INPUT", "TEXTAREA", "SELECT"]'), "缺输入控件判据");
});

test("卡片 portal 到 body 并随画布移动重锚（祖先 main 的 contain: paint 会给 fixed 建包含块）", () => {
  const src = read("../src/components/task/WorkflowCanvas.tsx");
  assert.ok(src.includes("createPortal("), "卡片必须 portal 出去");
  assert.ok(src.includes("document.body"), "portal 目标是 body");
  assert.ok(src.includes("onMove={handleViewportMove}"), "画布平移/缩放后必须重新锚定卡片");
  assert.ok(src.includes("nodeScreenRect(document, selectedId)"), "重锚要按 data-id 重新量节点框");
});

test("左侧草稿卡只显示名字，描述退到 title 里", () => {
  const src = read("../src/components/task/WorkflowDraftEditor.tsx");
  assert.ok(!src.includes("opacity: 0.75"), "草稿卡里不该再有第二行描述");
  assert.ok(/title=\{d\.description \?/.test(src), "描述要留在 title 提示里");
});

test("节点属性面板给出「另起会话」与「独立 worktree」两个开关", () => {
  const src = read("../src/components/task/WorkflowNodeInspector.tsx");
  assert.ok(src.includes('data-testid="workflow-node-new-session"'));
  assert.ok(src.includes('data-testid="workflow-node-independent-worktree"'));
  assert.ok(src.includes("setWorkflowNodeUsesOwnWorktree(selected.params"), "worktree 开关必须写 worktree=independent");
});
