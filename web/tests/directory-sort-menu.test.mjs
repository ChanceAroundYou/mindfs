import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

const read = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

test("directory sort menu keeps inherit and explicit sorting in the same menu state", () => {
  const parent = read("../src/components/file/DefaultListViewMenu.tsx");
  const menu = read("../src/components/file/DirectorySortMenu.tsx");
  assert.match(parent, /<DirectorySortMenu[\s\S]{0,400}?closeMenu=\{\(\) => setIsMenuOpen\(false\)\}/);
  assert.match(menu, /onSortModeChange\?\.\("inherit"\)/);
  assert.match(menu, /DIRECTORY_SORT_OPTIONS\.map/);
  assert.match(menu, /sortControlValue === option\.value/);
  assert.match(menu, /closeMenu\(\);\s*setIsOpen\(false\)/);
  assert.match(menu, /aria-expanded=\{isOpen\}/);
});
