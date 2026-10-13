import assert from "node:assert/strict";
import { closeSync, openSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = new URL("../", import.meta.url);
const readPhysical = (path) => {
  const fd = openSync(resolve(root.pathname, path), "r");
  try { return readFileSync(fd, "utf8"); } finally { closeSync(fd); }
};
const hook = readPhysical("src/hooks/useMarkdownViewer.tsx");
const image = readPhysical("src/components/common/MarkdownImage.tsx");

assert.ok(hook.split("\n").length - 1 < 500, "the markdown hook stays below the frontend hard line");
assert.match(hook, /import \{ MarkdownImage \} from "\.\.\/components\/common\/MarkdownImage"/);
assert.match(hook, /<MarkdownImage[\s\S]*?currentPath=\{currentPath\}[\s\S]*?root=\{root\}/);
assert.match(hook, /export function useMarkdownViewer/);
assert.match(image, /fetchProofProtectedBlob\(\{ rootId: root, path: resolvedPath \}\)/);
assert.match(image, /URL\.createObjectURL\(blob\)/);
assert.match(image, /URL\.revokeObjectURL\(objectURL\)/);
assert.match(image, /isDirectImageSrc\(trimmedSrc\)/);

console.log("markdown-viewer-split.test.mjs: OK");
