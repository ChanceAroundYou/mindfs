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
