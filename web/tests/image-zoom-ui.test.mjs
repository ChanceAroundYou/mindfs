import { test } from "vitest";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

/**
 * 图片缩放（G-KK）的接线守卫。
 *
 * 钉的是「谁负责哪一层」：数学在 shared/zoomTransform（旧名门面 diagramZoom 保留）、
 * 手势在 hooks/usePinchZoom、灯箱在 components/common/ImageZoomOverlay、两处消费面
 * （文件视图 / 会话 Markdown）各自只做接线。合上游时这几处被整体替换，测试就会红。
 */
test("zoom math is shared and the diagram facade keeps its old names", () => {
  const math = read("../src/shared/zoomTransform.ts");
  const facade = read("../src/shared/diagramZoom.ts");
  const diagram = read("../src/components/common/DiagramPreview.tsx");
  const gesture = read("../src/hooks/usePinchZoom.ts");

  assert.match(math, /export const ZOOM_MIN_SCALE = 0\.25/);
  assert.match(math, /export const ZOOM_MAX_SCALE = 16/);
  assert.match(math, /export function zoomAt\(/);
  assert.match(math, /export function gestureMetrics\(/);
  // map / 缩放可逆：比例按新 scale/旧 scale 算，锚点才不会漂。
  assert.match(math, /const ratio = scale \/ transform\.scale/);

  // 图表预览不动（真机验过），只把数学搬走；旧名字必须还在，否则它当场坏掉。
  assert.match(facade, /export \{ gestureMetrics as diagramGesture, zoomAt as zoomDiagramAt \}/);
  assert.match(diagram, /from "\.\.\/\.\.\/shared\/diagramZoom"/);

  // 手势：滚轮只在 Ctrl/⌘ 时接管（裸滚轮是页面滚动），双指按两指距离比例缩放。
  assert.match(gesture, /if \(!event\.ctrlKey && !event\.metaKey\) return;/);
  assert.match(gesture, /setPointerCapture/);
  assert.match(gesture, /after\.distance \/ before\.distance/);
  assert.match(gesture, /onDoubleClick/);
});

test("image surfaces: inline pinch zoom in the file viewer, lightbox for chat images", () => {
  const viewer = read("../src/components/file/ImageViewer.tsx");
  const markdown = read("../src/components/common/MarkdownImage.tsx");
  const overlay = read("../src/components/common/ImageZoomOverlay.tsx");

  // 文件视图：内联缩放 + 版本号 + 灯箱入口。
  assert.match(viewer, /import \{ usePinchZoom \} from "\.\.\/\.\.\/hooks\/usePinchZoom"/);
  assert.match(viewer, /import \{ ImageZoomOverlay \} from "\.\.\/common\/ImageZoomOverlay"/);
  assert.match(viewer, /version\?: string/);
  // 未缩放时把手势还给页面滚动，放大后才独占（否则拖动会连带滚动父容器）。
  assert.match(viewer, /touchAction: zoomed \? "none" : "pan-y"/);
  assert.match(viewer, /prev\.version === next\.version/);

  // 会话图片：点图开灯箱（同时把点击从可能包着它的链接上摘下来）。
  assert.match(markdown, /import \{ ImageZoomOverlay \} from "\.\.\/common\/ImageZoomOverlay"/);
  assert.match(markdown, /event\.stopPropagation\(\);\n\s+setZoomed\(true\);/);
  assert.match(markdown, /<ImageZoomOverlay src=\{resolvedSrc\}/);

  // 灯箱：modal dialog + portal，Escape 关闭、锁 body 滚动、关闭后还焦点。
  assert.match(overlay, /createPortal\(/);
  assert.match(overlay, /node\.showModal\(\)/);
  assert.match(overlay, /onCancel=\{\(event\) => \{ event\.preventDefault\(\); onClose\(\); \}\}/);
  assert.match(overlay, /document\.body\.style\.overflow = "hidden"/);
  assert.match(overlay, /previousFocus instanceof HTMLElement\) previousFocus\.focus\(\)/);
  assert.match(overlay, /ResizeObserver/);
});

test("image viewer strings exist in both locales", () => {
  const zh = read("../src/i18n/locales/zh-CN-projects.ts");
  const en = read("../src/i18n/locales/en-US-projects.ts");
  const keys = ["preview", "zoomIn", "zoomOut", "reset", "expand", "gestures"];
  for (const key of keys) {
    assert.match(zh, new RegExp(`"imageViewer\\.${key}":`), `zh-CN missing imageViewer.${key}`);
    assert.match(en, new RegExp(`"imageViewer\\.${key}":`), `en-US missing imageViewer.${key}`);
  }
  for (const key of ["reload", "reloading", "reloadFailed"]) {
    assert.match(zh, new RegExp(`"fileViewer\\.${key}":`), `zh-CN missing fileViewer.${key}`);
    assert.match(en, new RegExp(`"fileViewer\\.${key}":`), `en-US missing fileViewer.${key}`);
  }
});
