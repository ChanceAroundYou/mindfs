import { test } from "node:test";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";

const read = (path) => rawReadFileSync(new URL(path, import.meta.url), "utf8");

test("FileTree delegates its header menu and overlay presentation as one view module", () => {
  const tree = read("../src/components/file/FileTree.tsx");
  const overlays = read("../src/components/file/FileTreeHeaderOverlays.tsx");

  assert.match(tree, /<FileTreeHeaderOverlays[\s\S]*ctx=\{\{/);
  assert.doesNotMatch(tree, /<FileTreeSettingsMenu|<AgentConfigPopover|<FileTreeSessionNamingPopover/);
  assert.match(overlays, /<FileTreeSettingsMenu/);
  assert.match(overlays, /<AgentConfigPopover/);
  assert.match(overlays, /<FileTreeSessionNamingPopover/);
  assert.match(overlays, /<AgentLifecyclePopover/);
});
