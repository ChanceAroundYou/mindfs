import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import { test } from "vitest";

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

test("task inline editor delegates candidate rendering and selection to its menu", () => {
  const parent = read("../src/components/app/TaskInlineEditPanel.tsx");
  const menu = read("../src/components/app/TaskInlineCandidateMenu.tsx");
  assert.match(parent, /<TaskInlineCandidateMenu[\s\S]{0,500}?onSelect=\{applyTaskInlineCandidate\}/);
  assert.match(parent, /visible=\{!!taskInlineActiveToken\}/);
  assert.match(menu, /visible \|\| candidates\.length === 0/);
  assert.match(menu, /event\.preventDefault\(\);\s*onSelect\(candidate\)/);
  assert.match(menu, /candidate\.type === "file" \? `@\$\{candidate\.name\}`/);
  assert.match(menu, /onMouseEnter=\{\(\) => onSelectedIndexChange\(index\)\}/);
});
