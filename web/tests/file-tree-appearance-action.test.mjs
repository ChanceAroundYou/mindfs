import { test } from "node:test";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";

const read = (path) => rawReadFileSync(new URL(path, import.meta.url), "utf8");

test("FileTree appearance view delegates persistence to its hook", () => {
  const view = read("../src/components/file/FileTree.tsx");
  const options = read("../src/components/file/FileTreeAppearanceOptions.tsx");
  const menuOptions = read("../src/components/file/fileTreeMenuOptions.ts");
  const hook = read("../src/components/file/useFileTreeAppearance.ts");
  const service = read("../src/services/prefs/appearance.ts");
  const types = read("../src/shared/appearanceTypes.ts");

  assert.doesNotMatch(view, /services\/prefs\/appearance/);
  assert.match(view, /useFileTreeAppearance\(\)/);
  assert.match(options, /onChange\(option\.value\)/);
  assert.doesNotMatch(options, /services\/prefs\/appearance/);
  assert.doesNotMatch(menuOptions, /services\/prefs\/appearance/);
  assert.match(hook, /setAppearanceMode\(mode\)/);
  assert.match(service, /export type \{ AppearanceMode \} from "\.\.\/\.\.\/shared\/appearanceTypes"/);
  assert.match(types, /export type AppearanceMode/);
});
