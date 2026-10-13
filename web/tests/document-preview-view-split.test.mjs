import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

test("document viewer delegates PDF virtualization and keeps shared preview errors", () => {
  const viewer = read("../src/components/file/DocumentViewer.tsx");
  const pdf = read("../src/components/file/PdfPreview.tsx");
  const errors = read("../src/components/file/DocumentPreviewError.tsx");
  assert.match(viewer, /import \{ PdfPreview \} from "\.\/PdfPreview"/);
  assert.match(viewer, /describeDocumentPreviewError as describeError/);
  assert.match(pdf, /visiblePageRange\(/);
  assert.match(pdf, /new ResizeObserver\(sync\)/);
  assert.match(pdf, /Math\.min\(window\.devicePixelRatio \|\| 1, 2\)/);
  assert.match(pdf, /canvas\.width = 0/);
  assert.match(errors, /fileViewer\.previewFailedReason/);
  assert.match(errors, /message\.length > 200/);
});
