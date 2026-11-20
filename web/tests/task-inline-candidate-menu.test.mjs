import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import { test } from "vitest";

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

test("task inline editor delegates candidate rendering and selection to its menu", () => {
  const parent = read("../src/components/app/TaskInlineEditPanel.tsx");
  const menu = read("../src/components/app/TaskInlineCandidateMenu.tsx");
  assert.match(parent, /<TaskInlineCandidateMenu[\s\S]{0,500}?onSelect=\{applyTaskInlineCandidate\}/);
  assert.match(parent, /visible=\{!!taskInlineActiveToken\}/);
  assert.match(menu, /const active = visible && candidates\.length > 0/);
  assert.match(menu, /event\.preventDefault\(\);\s*onSelect\(candidate\)/);
  assert.match(menu, /candidate\.type === "file" \? `@\$\{candidate\.name\}`/);
  assert.match(menu, /onMouseEnter=\{\(\) => onSelectedIndexChange\(index\)\}/);
});

/**
 * 2026-11-20 用户报「@ 候选菜单在新建任务面板里被遮挡，移动端几乎看不见」。
 *
 * 现场实测（隔离实例 + 真浏览器，1440×900 与 375×667 各一次）：菜单是
 * `position:absolute; bottom:calc(100% + 6px)` 挂在包住编辑器的 relative 容器里，
 * 而它的祖先是 `.mindfs-panel-body`（`overflow:auto`）和 `.mindfs-panel-dialog`
 * （`overflow:hidden`）。面板正文顶端到编辑器顶端只有约 40px，4 条候选里前 3 条的
 * 命中测试落在 `.mindfs-panel-overlay` / 面板 header 上，移动端菜单顶端甚至是 -16px。
 *
 * 修法：portal 到 body + 按锚点矩形 fixed 定位。这两条都是硬约束，回归时必须红。
 */
test("candidate menu escapes the panel scroll and clip layers", () => {
  const menu = read("../src/components/app/TaskInlineCandidateMenu.tsx");
  const panel = read("../src/components/app/TaskInlineEditPanel.tsx");
  const detail = read("../src/components/task/ActivityInput.tsx");

  assert.match(menu, /createPortal\(/);
  assert.match(menu, /document\.body/);
  assert.match(menu, /position: "fixed"/);
  assert.doesNotMatch(menu, /bottom: "calc\(100% \+ 6px\)"/);
  // 软键盘只改 visualViewport，不改布局视口：可用高度必须按视觉视口算。
  assert.match(menu, /window\.visualViewport/);

  // 两处调用点都必须把编辑器外层容器作为锚点传进去，否则量不到位置。
  assert.match(panel, /ref=\{composerAnchorRef\}[\s\S]{0,400}?anchorRef=\{composerAnchorRef\}/);
  assert.match(detail, /ref=\{composerAnchorRef\}[\s\S]{0,400}?anchorRef=\{composerAnchorRef\}/);
});
