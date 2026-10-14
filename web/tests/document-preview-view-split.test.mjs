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
  assert.match(pdf, /visiblePageRange\(/);
  assert.match(pdf, /new ResizeObserver\(sync\)/);
  assert.match(pdf, /Math\.min\(window\.devicePixelRatio \|\| 1, 2\)/);
  assert.match(pdf, /canvas\.width = 0/);
  assert.match(errors, /fileViewer\.previewFailedReason/);
  assert.match(shared, /message\.length > 200/);
  assert.match(hook, /fetchProofProtectedBlob/);
  assert.match(hook, /cancelled/);
  assert.match(hook, /status: "error", detail: "no project"/);
});
