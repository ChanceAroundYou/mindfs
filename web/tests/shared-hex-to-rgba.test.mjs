import { test } from "vitest";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";
import { hexToRgba } from "../src/shared/hexToRgba.ts";

const read = (path) => rawReadFileSync(new URL(path, import.meta.url), "utf8");

test("shared hex color conversion preserves shorthand, fallback, and rgba pass-through", () => {
  assert.equal(hexToRgba("#3b82f6", 0.25), "rgba(59, 130, 246, 0.25)");
  assert.equal(hexToRgba("#abc", 0.5), "rgba(170, 187, 204, 0.5)");
  assert.equal(hexToRgba("not-a-color", 0.2), "rgba(59, 130, 246, 0.2)");
  assert.equal(hexToRgba("rgba(1, 2, 3, 0.4)", 0.9), "rgba(1, 2, 3, 0.4)");
  const facade = read("../src/app/taskIcons.ts");
  assert.match(facade, /export \{ hexToRgba as hexToRgbaApp \} from "\.\.\/shared\/hexToRgba"/);
});

test("FileTree uses the shared color helper and the app path remains a facade", () => {
  const fileTree = read("../src/components/file/FileTree.tsx");
  const facade = read("../src/app/taskIcons.ts");
  assert.match(fileTree, /from "\.\.\/\.\.\/shared\/hexToRgba"/);
  assert.doesNotMatch(fileTree, /from "\.\.\/\.\.\/app\/taskIcons"/);
  assert.match(facade, /hexToRgba as hexToRgbaApp/);
});
