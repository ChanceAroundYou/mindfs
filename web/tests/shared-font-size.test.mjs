import { test } from "node:test";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";
import { changeFontSize, DEFAULT_FONT_SIZE_PREFERENCES, FONT_SIZE_MAX, FONT_SIZE_MIN } from "../src/shared/fontSize.ts";

test("font size adjustment stays pure and clamps to the shared bounds", () => {
  const before = { ...DEFAULT_FONT_SIZE_PREFERENCES };
  const increased = changeFontSize(before, "main", 1);
  assert.equal(increased.main, 1.1);
  assert.equal(before.main, 1);
  assert.equal(changeFontSize({ ...before, main: FONT_SIZE_MAX }, "main", 1).main, FONT_SIZE_MAX);
  assert.equal(changeFontSize({ ...before, main: FONT_SIZE_MIN }, "main", -1).main, FONT_SIZE_MIN);
});

test("FileTree uses shared font-size behavior and the service facade preserves exports", () => {
  const root = new URL("../src/", import.meta.url);
  const view = rawReadFileSync(new URL("components/file/FileTree.tsx", root), "utf8");
  const service = rawReadFileSync(new URL("services/prefs/fontSize.ts", root), "utf8");
  assert.match(view, /from "\.\.\/\.\.\/shared\/fontSize"/);
  assert.doesNotMatch(view, /from "\.\.\/\.\.\/services\/prefs\/fontSize"/);
  assert.match(service, /export \{[\s\S]*changeFontSize[\s\S]*\} from "\.\.\/\.\.\/shared\/fontSize"/);
});
