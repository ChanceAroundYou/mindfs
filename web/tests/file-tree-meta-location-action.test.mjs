import { test } from "node:test";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";

const read = (path) => rawReadFileSync(new URL(path, import.meta.url), "utf8");

test("FileTree delegates new-project meta preference updates to its logic hook", () => {
  const view = read("../src/components/file/FileTree.tsx");
  const logic = read("../src/components/file/useFileTreePreferences.ts");

  assert.doesNotMatch(view, /updateNewProjectMetaLocationPreference/);
  assert.match(view, /onClick=\{\(\) => \{ void toggleNewProjectMetaLocation\(\); \}\}/);
  assert.match(logic, /await updateNewProjectMetaLocationPreference\(next\)/);
  assert.match(logic, /catch \{\s*setNewProjectMetaLocation\(previous\)/);
  assert.match(logic, /finally \{\s*setNewProjectMetaLocationBusy\(false\)/);
});
