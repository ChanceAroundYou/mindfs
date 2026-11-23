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

test("anchored menu prefers above when the composer sits at the panel bottom", () => {
  const position = computeAnchoredMenuPosition({
    anchor: { top: 500, bottom: 530, left: 700, right: 740 },
    menu: { width: 176, height: 120 },
    viewport,
    prefer: "above",
  });
  assert.equal(position.top, 500 - 120 - 8);
});

test("prefer above still flips below when there is not enough room above", () => {
  const position = computeAnchoredMenuPosition({
    anchor: { top: 100, bottom: 130, left: 700, right: 740 },
    menu: { width: 176, height: 120 },
    viewport,
    prefer: "above",
  });
  // 上方只有 100 - 16 = 84px，装不下 120px 的菜单；翻到下方。
  assert.equal(position.top, 130 + 8);
});

test("when neither side fits, the menu uses the roomier side and gets a height cap", () => {
  const position = computeAnchoredMenuPosition({
    anchor: { top: 300, bottom: 330, left: 700, right: 740 },
    menu: { width: 176, height: 500 },
    viewport,
    prefer: "above",
  });
  // 上方 284px / 下方 454px：取下方，maxHeight 正好夹在视口边缘内。
  assert.equal(position.top, 330 + 8);
  assert.equal(position.maxHeight, 454);
  assert.ok(position.top + position.maxHeight <= viewport.height - 8);
});

test("a menu wider than the viewport is clamped left and reports the usable width", () => {
  const narrow = { top: 0, left: 0, width: 375, height: 667 };
  const position = computeAnchoredMenuPosition({
    anchor: { top: 500, bottom: 530, left: 300, right: 340 },
    menu: { width: 400, height: 120 },
    viewport: narrow,
    prefer: "above",
  });
  assert.equal(position.left, 8);
  assert.equal(position.maxWidth, 375 - 16);
});

test("default prefer is below when both sides have room", () => {
  const position = computeAnchoredMenuPosition({
    anchor: { top: 500, bottom: 530, left: 700, right: 740 },
    menu: { width: 176, height: 120 },
    viewport,
  });
  // 没有显式 prefer 时仍是 Select / 文件菜单一直以来的「优先下方」。
  assert.equal(position.top, 530 + 8);
});

test("the default placement keeps the pre-existing values unchanged", () => {
  const position = computeAnchoredMenuPosition({
    anchor: { top: 100, bottom: 130, left: 900, right: 940 },
    menu: { width: 176, height: 120 },
    viewport,
  });
  assert.equal(position.left, 764);
  assert.equal(position.top, 138);
  assert.equal(position.maxHeight, 654);
  assert.equal(position.maxWidth, 984);
});

console.log("anchored-menu-position.test.mjs: OK");
