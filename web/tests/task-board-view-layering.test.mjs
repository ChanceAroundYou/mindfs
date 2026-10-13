import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const view = readFileSync(new URL("../src/components/task/TaskBoardView.tsx", import.meta.url), "utf8");
const storage = readFileSync(new URL("../src/app/appStorage.ts", import.meta.url), "utf8");
const valueImports = [...view.matchAll(/^import[\s\S]*?;/gm)]
  .map(([statement]) => statement)
  .filter((statement) => !/^import\s+type\b/.test(statement));

test("TaskBoardView avoids runtime app/storage dependencies", () => {
  assert.equal(valueImports.some((statement) => /from ["'][^"']*services\//.test(statement)), false);
  assert.equal(valueImports.some((statement) => /from ["'][^"']*app\/(?:appMisc|appStorage|use[A-Z])/.test(statement)), false);
  assert.match(view, /from ["']\.\.\/\.\.\/hooks\/useResponsive["']/);
  assert.match(view, /from ["']\.\.\/\.\.\/shared\/taskTemplateConstants["']/);
  // taskIcons is a legacy presenter asset module: SVGs and CSS-in-JS only, no app state or data access.
  assert.match(view, /from ["']\.\.\/\.\.\/app\/taskIcons["']/);
});

test("appStorage preserves the public task-template filter facade", () => {
  assert.match(storage, /export \{ TASK_TEMPLATE_ALL_FILTER \} from ["']\.\.\/shared\/taskTemplateConstants["']/);
});
