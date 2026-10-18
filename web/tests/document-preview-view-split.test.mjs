import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import { test } from "vitest";

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

test("document viewer delegates PDF virtualization and keeps shared preview errors", () => {
  const viewer = read("../src/components/file/DocumentViewer.tsx");
  const pdf = read("../src/components/file/PdfPreview.tsx");
  const errors = read("../src/components/file/DocumentPreviewError.tsx");
  const shared = read("../src/shared/documentPreview.ts");
  const hook = read("../src/components/file/useDocumentPreviewBlob.ts");
  assert.match(viewer, /import \{ PdfPreview \} from "\.\/PdfPreview"/);
  assert.doesNotMatch(viewer, /services\/file/);
  assert.match(viewer, /useDocumentPreviewBlob\(root, path\)/);
  assert.match(viewer, /describeDocumentPreviewError as describeError/);
  // 页虚拟化由 IntersectionObserver 驱动（commit d5964631）：手算 scrollTop 认不出真正的滚动容器，
  // 中间页会白屏。钉这一条 = 钉「按可见性渲染」这个机制本身。
  assert.match(pdf, /new IntersectionObserver\(/);
  // 注意：d5964631 同时删掉了原来的 ResizeObserver(sync)，所以「容器尺寸变化后重渲染」这条
  // 现在没有任何守卫。窗口改宽高时页宽会停在旧值（滚出去再滚回来才重画）。要不要补由用户定。
  assert.match(pdf, /Math\.min\(window\.devicePixelRatio \|\| 1, 2\)/);
  assert.match(pdf, /canvas\.width = 0/);
  assert.match(errors, /fileViewer\.previewFailedReason/);
  assert.match(shared, /message\.length > 200/);
  assert.match(hook, /fetchProofProtectedBlob/);
  assert.match(hook, /cancelled/);
  assert.match(hook, /status: "error", detail: "no project"/);
});
