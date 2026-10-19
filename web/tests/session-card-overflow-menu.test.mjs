import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import { test } from "vitest";

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

test("session card keeps overflow actions in a focused menu with parent-owned state", () => {
  const card = read("../src/components/session/SessionCard.tsx");
  const menu = read("../src/components/session/SessionCardOverflowMenu.tsx");
  assert.match(card, /<SessionCardOverflowMenu[\s\S]{0,700}?onArchive=\{onArchive\}/);
  assert.match(card, /onRename=\{\(name\) => \{[\s\S]{0,100}?setEditing\(true\)/);
  for (const key of ["sessionList.pin", "sessionList.sync", "sessionList.rename", "sessionList.archive", "common.delete"]) {
    assert.ok(menu.includes(`t("${key}")`), `overflow menu keeps ${key}`);
  }
  assert.match(menu, /void onPin\?\.\(session, !isPinned\)/);
  assert.match(menu, /disabled=\{syncing\}/);
  assert.match(menu, /void onArchive\(session, !isArchived\)/);
});

// 列表最后几行点「⋯」时下方放不下，弹层继续往下就撑出视口（用户 2026-10-19 报）。
// 判据必须是「实测剩余空间」，不能写死方向 —— 写死 top 就是原缺陷本身。
test("session card overflow menu flips up when there is no room below", () => {
  const menu = read("../src/components/session/SessionCardOverflowMenu.tsx");

  assert.match(
    menu,
    /openUp \? \{ bottom: "calc\(100% \+ 6px\)" \} : \{ top: "calc\(100% \+ 6px\)" \}/,
    "the menu must be positioned above or below the trigger, not always below",
  );
  assert.match(
    menu,
    /const below = bounds\.bottom - rect\.bottom - 6;/,
    "the flip decision must measure the space actually left below the trigger",
  );
  // 空间要按裁剪祖先量：卡片住在列表那个 `flex:1; overflow:auto` 容器里，越过它就被切。
  // 拿 window.innerHeight 当边界会高估下方空间，该翻的时候不翻 —— 那就是原缺陷没修掉。
  assert.match(
    menu,
    /getComputedStyle\(node\)\.overflowY !== "visible"/,
    "the available space must be measured against the clipping ancestor, not the viewport",
  );
  assert.doesNotMatch(
    menu,
    /const below = window\.innerHeight/,
    "window.innerHeight is the wrong reference box — the scroll container clips the menu first",
  );
  assert.match(
    menu,
    /setOpenUp\(below < box\.offsetHeight && above > below\)/,
    "flip only when it does not fit below AND above is roomier — same rule as ProviderModelSelect",
  );
  // 弹层跟着行滚动，方向要跟着重算，否则滚一下又跑出视口。
  assert.match(
    menu,
    /window\.addEventListener\("scroll", place, true\)/,
    "the placement must be recomputed on scroll",
  );
  assert.match(
    menu,
    /window\.removeEventListener\("scroll", place, true\)/,
    "the scroll listener must be removed on close/unmount",
  );
  assert.doesNotMatch(
    menu,
    /\n\s+top: "calc\(100% \+ 6px\)",\n/,
    "the old unconditional downward placement must be gone",
  );
});
