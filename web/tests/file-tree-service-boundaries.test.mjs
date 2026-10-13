import { test } from "node:test";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";

const read = (path) => rawReadFileSync(new URL(path, import.meta.url), "utf8");

test("FileTree delegates bootstrap and node-registry reads to colocated logic hooks", () => {
  const view = read("../src/components/file/FileTree.tsx");
  const readiness = read("../src/components/file/useFileTreeProtectedAPI.ts");
  const accent = read("../src/components/file/useFileTreeProjectTabAccent.ts");
  const effects = read("../src/components/file/useFileTreeEffects.ts");
  const tabs = read("../src/components/file/FileTreeProjectTabs.tsx");
  const entries = read("../src/components/file/fileTreeEntries.tsx");
  const palette = read("../src/shared/nodePalette.ts");
  const registry = read("../src/services/net/nodeRegistry.ts");

  assert.doesNotMatch(view, /services\/(?:net\/bootstrap|net\/nodeRegistry)/);
  assert.match(view, /useFileTreeProtectedAPI\(\)/);
  assert.match(view, /useFileTreeProjectTabAccent\(rootColor, entries, rootId\)/);
  assert.match(readiness, /bootstrapService\.subscribe/);
  assert.match(readiness, /bootstrapService\.canUseProtectedAPI/);
  assert.match(accent, /getNodes\(\)/);
  assert.match(entries, /from "\.\.\/\.\.\/shared\/nodePalette"/);
  assert.match(palette, /DEFAULT_NODE_COLOR = PALETTE\[0\]/);
  assert.match(registry, /export \{ DEFAULT_NODE_COLOR, PALETTE \} from "\.\.\/\.\.\/shared\/nodePalette"/);
  assert.doesNotMatch(effects, /bootstrapService/);
  assert.doesNotMatch(tabs, /getNodes|DEFAULT_NODE_COLOR|services\//);
  assert.match(tabs, /const tabAccent = accentColor/);
});
