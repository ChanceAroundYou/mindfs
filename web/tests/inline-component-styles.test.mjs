import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { MODULES } from "./source-map.mjs";

const read = (path) => readFileSync(new URL(`../${path}`, import.meta.url), "utf8");
const fileEditor = read("src/components/file/FileEditor.tsx");
const diagramPreview = read("src/components/common/DiagramPreview.tsx");

assert.match(fileEditor, /const FILE_EDITOR_STYLES = String\.raw`/);
assert.match(fileEditor, /<style>\{FILE_EDITOR_STYLES\}<\/style>/);
assert.match(fileEditor, /\.file-editor-dialog::backdrop/);
assert.match(fileEditor, /\.file-code-editor \.cm-scroller/);
assert.match(fileEditor, /@media \(prefers-color-scheme: dark\)/);
assert.match(diagramPreview, /const DIAGRAM_PREVIEW_STYLES = String\.raw`/);
assert.match(diagramPreview, /<style>\{DIAGRAM_PREVIEW_STYLES\}<\/style>/);
assert.match(diagramPreview, /\.diagram-preview::backdrop/);
assert.match(diagramPreview, /\.diagram-preview-viewport:active/);
assert.match(diagramPreview, /env\(safe-area-inset-bottom/);
assert.doesNotMatch(fileEditor, /import ["']\.\/FileEditor\.css/);
assert.doesNotMatch(diagramPreview, /import ["']\.\/DiagramPreview\.css/);
assert.equal(existsSync(new URL("../src/components/file/FileEditor.css", import.meta.url)), false);
assert.equal(existsSync(new URL("../src/components/common/DiagramPreview.css", import.meta.url)), false);
assert.deepEqual(MODULES["src/components/FileEditor.css"], ["src/components/file/FileEditor.tsx"]);
assert.deepEqual(MODULES["src/components/DiagramPreview.css"], ["src/components/common/DiagramPreview.tsx"]);

console.log("inline-component-styles.test.mjs: OK");
