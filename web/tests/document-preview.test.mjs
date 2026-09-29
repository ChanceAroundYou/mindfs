import assert from "node:assert/strict";
import { getDocumentPreviewKind, visiblePageRange, PDF_RENDER_MARGIN_PX } from "../src/services/documentPreview.ts";

assert.equal(getDocumentPreviewKind(".PDF"), "pdf");
assert.equal(getDocumentPreviewKind(".pdf"), "pdf");
assert.equal(getDocumentPreviewKind("", "application/pdf"), "pdf");
assert.equal(getDocumentPreviewKind(".PDF", "application/pdf"), "pdf");
assert.equal(getDocumentPreviewKind(".docx"), "word");
assert.equal(getDocumentPreviewKind(".xlsx"), "excel");
assert.equal(getDocumentPreviewKind(".pptx"), "powerpoint");
assert.equal(getDocumentPreviewKind("", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"), "word");
assert.equal(getDocumentPreviewKind(".doc"), null);
assert.equal(getDocumentPreviewKind(".txt", "text/plain"), null);

// --- visiblePageRange：只渲染视口附近的页 ---
const uniform = (count, height = 800) => Array.from({ length: count }, () => ({ width: 612, height }));
const GAP = 18;

assert.deepEqual(visiblePageRange([], 0, 800, GAP), { start: 0, end: -1 });

// 视口在顶部：只覆盖视口 + 上下留白
{
  const layouts = uniform(100);
  const { start, end } = visiblePageRange(layouts, 0, 800, GAP, 0);
  assert.equal(start, 0, "首屏应从第 1 页开始");
  // 800px 视口装得下 1 个 800px 高的页面：首屏就是第 1 页
  assert.equal(end, 0);
  assert.ok(end < 10, `首屏不该一次画太多页，实际 ${end + 1}`);
}

// 留白让快速滚动不至于白屏，但仍然有界
{
  const layouts = uniform(100);
  const { start, end } = visiblePageRange(layouts, 0, 800, GAP);
  assert.ok(end - start < 20, `带留白也应只覆盖有限页，实际 ${end - start + 1}`);
  assert.equal(PDF_RENDER_MARGIN_PX > 0, true);
}

// 滚到中段：区间随 scrollTop 前移
{
  const layouts = uniform(100);
  const top = visiblePageRange(layouts, 0, 800, GAP, 0);
  const mid = visiblePageRange(layouts, 20 * (800 + GAP), 800, GAP, 0);
  assert.ok(mid.start > top.start, "滚动后起始页应前移");
  assert.ok(mid.start >= 18 && mid.start <= 22, `中段起点应在 20 页附近，实际 ${mid.start}`);
}

// 滚到底：最后一页必须在区间内
{
  const layouts = uniform(100);
  const total = 100 * (800 + GAP);
  const { end } = visiblePageRange(layouts, total, 800, GAP, 0);
  assert.equal(end, 99, "滚到底应渲染最后一页");
}

// 高度不齐（真实 PDF 常见）：区间仍覆盖所有与视口相交的页
{
  const layouts = [{ width: 612, height: 1000 }, { width: 612, height: 200 }, { width: 612, height: 3000 }];
  const { start, end } = visiblePageRange(layouts, 0, 1500, GAP, 0);
  assert.equal(start, 0);
  assert.ok(end >= 1, "应覆盖到第二页");
}

// 滚动越界（内容尚未布局完）：不能返回空区间，否则首屏什么都不画
{
  const layouts = uniform(10);
  const { start, end } = visiblePageRange(layouts, 999999, 800, GAP);
  assert.ok(end >= start, "越界时也要给出可渲染的页");
}

console.log("document-preview: ok");
