import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

const sourcePath = path.resolve("src/services/bottomSheetModel.ts");
const source = fs.readFileSync(sourcePath, "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText;
const sandbox = { exports: {}, module: { exports: {} } };
vm.runInNewContext(compiled, sandbox, { filename: sourcePath });

const { BOTTOM_SHEET_DRAG_START_PX, BOTTOM_SHEET_EDGE_RATIO, resolveBottomSheetRelease } =
  sandbox.exports;

assert.equal(BOTTOM_SHEET_DRAG_START_PX, 8);
assert.equal(BOTTOM_SHEET_EDGE_RATIO, 0.2);

assert.equal(
  resolveBottomSheetRelease({ clientY: 150, viewportHeight: 1000, dragged: true }),
  "expand",
);
assert.equal(
  resolveBottomSheetRelease({ clientY: 850, viewportHeight: 1000, dragged: true }),
  "close",
);
assert.equal(
  resolveBottomSheetRelease({ clientY: 500, viewportHeight: 1000, dragged: true }),
  "half",
);
assert.equal(
  resolveBottomSheetRelease({ clientY: 100, viewportHeight: 1000, dragged: false }),
  "half",
);

// Source contracts that must initially fail until Task 4 wires BottomSheet.tsx.
const sheet = fs.readFileSync(path.resolve("src/components/BottomSheet.tsx"), "utf8");
assert.match(sheet, /onPointerDown=\{handlePointerDown\}/);
assert.match(sheet, /onPointerMove=\{handlePointerMove\}/);
assert.match(sheet, /onPointerUp=\{handlePointerUp\}/);
assert.match(sheet, /onPointerCancel=\{handlePointerCancel\}/);
assert.doesNotMatch(sheet, /onClick=\{onExpand\}/);
assert.match(sheet, /contentRef\?: React\.RefObject<HTMLDivElement \| null>/);

console.log("bottom-sheet-model source contracts OK");
