import assert from "node:assert/strict";
import { test } from "vitest";
import { computeAnchoredMenuPosition } from "../src/shared/anchoredMenuPosition.ts";

const viewport = { top: 0, left: 0, width: 1000, height: 800 };

test("anchored menu drops below the anchor when there is room", () => {
  const position = computeAnchoredMenuPosition({
    anchor: { top: 100, bottom: 130, left: 900, right: 940 },
    menu: { width: 176, height: 120 },
    viewport,
  });
  // 右边缘对齐锚点右边缘 + 8px 间距；下方放得下就放下方。
  assert.equal(position.left, 940 - 176);
  assert.equal(position.top, 130 + 8);
});

test("anchored menu flips above when it does not fit below", () => {
  const position = computeAnchoredMenuPosition({
    anchor: { top: 700, bottom: 730, left: 100, right: 140 },
    menu: { width: 176, height: 200 },
    viewport,
  });
  assert.equal(position.top, 700 - 200 - 8);
});

test("anchored menu is clamped inside the viewport", () => {
  const position = computeAnchoredMenuPosition({
    anchor: { top: 100, bottom: 130, left: 20, right: 60 },
    menu: { width: 176, height: 120 },
    viewport,
  });
  // 锚点贴着视口左边，菜单不能溢出（left 下限 = viewport.left + margin）。
  assert.ok(position.left >= 8);
  const right = computeAnchoredMenuPosition({
    anchor: { top: 100, bottom: 130, left: 960, right: 995 },
    menu: { width: 176, height: 120 },
    viewport,
  });
  assert.ok(right.left + 176 <= 1000 - 8);
});

test("anchored menu can align to the left edge of the anchor", () => {
  const position = computeAnchoredMenuPosition({
    anchor: { top: 100, bottom: 130, left: 300, right: 340 },
    menu: { width: 176, height: 120 },
    viewport,
    align: "left",
  });
  assert.equal(position.left, 300);
});

console.log("anchored-menu-position.test.mjs: OK");
