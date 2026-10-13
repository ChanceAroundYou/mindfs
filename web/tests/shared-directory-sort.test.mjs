import { test } from "node:test";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";
import { DIRECTORY_SORT_OPTIONS, sortDirectoryEntries } from "../src/shared/directorySort.ts";
import { sortDirectoryEntries as sortThroughService } from "../src/services/prefs/directorySort.ts";

const entries = [
  { name: "zeta", path: "/zeta", is_dir: false, size: 4, mtime: "2026-10-12T00:00:00Z" },
  { name: "10", path: "/10", is_dir: true, size: 2, mtime: "2026-10-10T00:00:00Z" },
  { name: "2", path: "/2", is_dir: false, size: 9, mtime: "2026-10-14T00:00:00Z" },
];

test("directory sorting stays pure, keeps directories first, and supports advertised modes", () => {
  const original = [...entries];
  assert.deepEqual(sortDirectoryEntries(entries, "name-asc").map(({ name }) => name), ["10", "2", "zeta"]);
  assert.deepEqual(sortDirectoryEntries(entries, "mtime-desc").map(({ name }) => name), ["10", "2", "zeta"]);
  assert.deepEqual(sortDirectoryEntries(entries, "size-desc").map(({ name }) => name), ["10", "2", "zeta"]);
  assert.deepEqual(entries, original);
  assert.deepEqual(DIRECTORY_SORT_OPTIONS.map(({ value }) => value), ["name-asc", "name-desc", "mtime-desc", "mtime-asc", "size-desc", "size-asc"]);
  assert.deepEqual(sortThroughService(entries, "size-asc"), sortDirectoryEntries(entries, "size-asc"));
});

test("FileTree settings and directory sort use shared ownership with a service facade", () => {
  const root = new URL("../src/", import.meta.url);
  const view = rawReadFileSync(new URL("components/file/FileTreeSettingsMenu.tsx", root), "utf8");
  const sort = rawReadFileSync(new URL("shared/directorySort.ts", root), "utf8");
  const facade = rawReadFileSync(new URL("services/prefs/directorySort.ts", root), "utf8");
  assert.match(view, /from "\.\.\/\.\.\/shared\/directorySort"/);
  assert.match(sort, /export function sortDirectoryEntries/);
  assert.match(facade, /export \* from "\.\.\/\.\.\/shared\/directorySort"/);
});
