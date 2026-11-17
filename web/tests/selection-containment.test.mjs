import { test } from "vitest";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";
import {
  clampSelectionToRoot,
  firstTextNode,
  isInsideRoot,
  lastTextNode,
  textBoundaries,
} from "../src/shared/selectionClamp.ts";

// 假节点：只用 nodeType / data / childNodes / contains，够 clampSelectionToRoot 走完全程。
function textNode(data) {
  const node = { nodeType: 3, data, childNodes: [], parentNode: null };
  node.contains = (other) => other === node;
  return node;
}

function elementNode(children = []) {
  const node = { nodeType: 1, childNodes: children, parentNode: null };
  for (const child of children) child.parentNode = node;
  node.contains = (other) => other === node || children.some((child) => child.contains(other));
  return node;
}

class FakeRange {
  constructor(startContainer, startOffset, endContainer, endOffset) {
    this.startContainer = startContainer;
    this.startOffset = startOffset;
    this.endContainer = endContainer;
    this.endOffset = endOffset;
    this.collapsed = startContainer === endContainer && startOffset === endOffset;
  }
  get commonAncestorContainer() {
    // 测试只给两层结构：外层容器 + 正文根，公共祖先就近取外层容器。
    return this.commonAncestorOverride ?? this.startContainer.parentNode ?? this.startContainer;
  }
  setStart(node, offset) {
    this.startContainer = node;
    this.startOffset = offset;
    this.collapsed = this.startContainer === this.endContainer && this.startOffset === this.endOffset;
  }
  setEnd(node, offset) {
    this.endContainer = node;
    this.endOffset = offset;
    this.collapsed = this.startContainer === this.endContainer && this.startOffset === this.endOffset;
  }
}

function fakeSelection(range) {
  const calls = { removed: 0, added: [] };
  return {
    calls,
    rangeCount: range ? 1 : 0,
    getRangeAt: () => range,
    removeAllRanges: () => { calls.removed += 1; },
    addRange: (next) => { calls.added.push(next); },
  };
}

function rootWithOwnerDocument(root) {
  root.ownerDocument = { createRange: () => new FakeRange(root, 0, root, 0) };
  return root;
}

test("边界端点跳过空文本节点，取正文真正的首尾", () => {
  const empty = textNode("");
  const first = textNode("开头");
  const middle = textNode("中间");
  const last = textNode("结尾");
  const root = elementNode([empty, first, elementNode([middle, last, textNode("")])]);

  assert.equal(firstTextNode(root), first);
  assert.equal(lastTextNode(root), last);
  assert.deepEqual(textBoundaries(root), {
    start: { node: first, offset: 0 },
    end: { node: last, offset: 2 },
  });
});

test("容器里没有文本时，边界退化成容器元素本身的起止偏移", () => {
  const root = elementNode([elementNode([])]);
  assert.equal(firstTextNode(root), null);
  assert.equal(lastTextNode(root), null);
  assert.deepEqual(textBoundaries(root), {
    start: { node: root, offset: 0 },
    end: { node: root, offset: 1 },
  });
});

test("isInsideRoot 认容器自身与后代，不认兄弟节点", () => {
  const inside = textNode("正文");
  const root = elementNode([inside]);
  const outside = textNode("标题");
  assert.equal(isInsideRoot(root, root), true);
  assert.equal(isInsideRoot(root, inside), true);
  assert.equal(isInsideRoot(root, outside), false);
  assert.equal(isInsideRoot(root, null), false);
});

test("两端都在正文里：常规选区不动", () => {
  const a = textNode("第一段");
  const b = textNode("第二段");
  const root = rootWithOwnerDocument(elementNode([a, b]));
  const range = new FakeRange(a, 0, b, 1);
  const selection = fakeSelection(range);

  assert.equal(clampSelectionToRoot(root, selection), false);
  assert.equal(selection.calls.removed, 0);
});

test("末端被拖到正文下方：末端拉回正文末尾", () => {
  const a = textNode("第一段");
  const b = textNode("第二段");
  const root = elementNode([a, b]);
  const composer = textNode("输入条");
  const outer = elementNode([root, composer]);
  rootWithOwnerDocument(root);
  const range = new FakeRange(a, 1, composer, 2);
  range.commonAncestorOverride = outer;
  const selection = fakeSelection(range);

  assert.equal(clampSelectionToRoot(root, selection), true);
  assert.equal(selection.calls.removed, 1);
  const next = selection.calls.added[0];
  assert.equal(next.startContainer, a);
  assert.equal(next.startOffset, 1);
  assert.equal(next.endContainer, b);
  assert.equal(next.endOffset, 3);
});

test("起点被拖到正文上方（标题栏）：起点拉回正文开头", () => {
  const title = textNode("会话标题");
  const a = textNode("第一段");
  const b = textNode("第二段");
  const outer = elementNode([elementNode([title]), elementNode([a, b])]);
  const root = rootWithOwnerDocument(outer.childNodes[1]);
  const range = new FakeRange(title, 2, b, 1);
  range.commonAncestorOverride = outer;
  const selection = fakeSelection(range);

  assert.equal(clampSelectionToRoot(root, selection), true);
  const next = selection.calls.added[0];
  assert.equal(next.startContainer, a);
  assert.equal(next.startOffset, 0);
  assert.equal(next.endContainer, b);
  assert.equal(next.endOffset, 1);
});

test("两端都在容器外：不属于本容器，一律不碰", () => {
  const title = textNode("会话标题");
  const sidebar = textNode("会话一");
  const root = rootWithOwnerDocument(elementNode([textNode("正文")]));
  const outer = elementNode([root]);
  const range = new FakeRange(title, 0, sidebar, 1);
  range.commonAncestorOverride = outer;
  const selection = fakeSelection(range);

  assert.equal(clampSelectionToRoot(root, selection), false);
  assert.equal(selection.calls.removed, 0);
});

test("折叠选区、空选区、空容器都不动手", () => {
  const a = textNode("第一段");
  const root = rootWithOwnerDocument(elementNode([a]));
  assert.equal(clampSelectionToRoot(root, fakeSelection(new FakeRange(a, 1, a, 1))), false);
  assert.equal(clampSelectionToRoot(root, fakeSelection(null)), false);
  assert.equal(clampSelectionToRoot(null, fakeSelection(new FakeRange(a, 0, a, 1))), false);
});

test("会话正文与面板正文都挂上了选区钳制，标题栏不可选", () => {
  const viewer = rawReadFileSync(new URL("../src/components/session/SessionViewer.tsx", import.meta.url), "utf8");
  const header = rawReadFileSync(new URL("../src/components/session/SessionViewerHeader.tsx", import.meta.url), "utf8");
  const panel = rawReadFileSync(new URL("../src/components/shell/PanelShell.tsx", import.meta.url), "utf8");
  const hook = rawReadFileSync(new URL("../src/hooks/useSelectionContainment.ts", import.meta.url), "utf8");

  // 会话滚动容器与面板正文各自钳制自己的选区；多视图并存时互不抢。
  assert.match(viewer, /import \{ useSelectionContainment \} from "\.\.\/\.\.\/hooks\/useSelectionContainment"/);
  assert.match(viewer, /useSelectionContainment\(scrollRef\)/);
  assert.match(viewer, /userSelect: "text"/);
  assert.match(panel, /useSelectionContainment\(bodyRef\)/);
  assert.match(panel, /<div ref=\{bodyRef\} className="mindfs-panel-body"/);

  // 标题栏里**一处** user-select:none 都不能有：只要拖选路径上有不可选区域，
  // Chromium 在「向上拖选 + 自动滚动」时会静默清空整个选区（实测 582 字 → 0，
  // 且不发 selectionchange）。越界选区只靠抬手时的钳制兜底。
  assert.doesNotMatch(header, /userSelect/);
  assert.doesNotMatch(panel, /userSelect: "none"/);

  // 监听挂在 document 的 selectionchange 上，卸载时摘掉。
  assert.match(hook, /document\.addEventListener\("selectionchange", clampSelection\)/);
  assert.match(hook, /document\.removeEventListener\("selectionchange", clampSelection\)/);
  assert.match(hook, /clampSelectionToRoot\(ref\.current, document\.getSelection\(\)\)/);
  // 拖选进行中不改写选区（会与浏览器的拖选状态打架，实测最终选区被清空）。
  assert.match(hook, /if \(pointerDown\) return;/);
  assert.match(hook, /document\.addEventListener\("pointerup", handlePointerRelease, true\)/);
  assert.match(hook, /document\.addEventListener\("pointerdown", handlePointerDown, true\)/);
});