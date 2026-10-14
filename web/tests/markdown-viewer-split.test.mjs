import assert from "node:assert/strict";
import { closeSync, openSync, readFileSync } from "./source-map-fs.mjs";
import { resolve } from "node:path";
import { test } from "vitest";

test("markdown-viewer-split", async () => {
const root = new URL("../", import.meta.url);
const readPhysical = (path) => {
  const fd = openSync(resolve(root.pathname, path), "r");
  try { return readFileSync(fd, "utf8"); } finally { closeSync(fd); }
};
const hook = readPhysical("src/hooks/useMarkdownViewer.tsx");
const image = readPhysical("src/components/common/MarkdownImage.tsx");
const blobHook = readPhysical("src/hooks/useProtectedBlobUrl.ts");

assert.ok(hook.split("\n").length - 1 < 500, "the markdown hook stays below the frontend hard line");
assert.match(hook, /import \{ MarkdownImage \} from "\.\.\/components\/common\/MarkdownImage"/);
assert.match(hook, /<MarkdownImage[\s\S]*?currentPath=\{currentPath\}[\s\S]*?root=\{root\}/);
assert.match(hook, /export function useMarkdownViewer/);
assert.match(image, /useProtectedBlobUrl\(/);
assert.match(image, /isDirectImageSrc\(trimmedSrc\)/);
assert.match(blobHook, /fetchProofProtectedBlob\(currentRequest\)/);
assert.match(blobHook, /URL\.createObjectURL\(blob\)/);
assert.match(blobHook, /URL\.revokeObjectURL\(objectURL\)/);

console.log("markdown-viewer-split.test.mjs: OK");
});
