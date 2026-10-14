import { test } from "vitest";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";

const read = (path) => rawReadFileSync(new URL(path, import.meta.url), "utf8");

test("FileTree view and props use shared contracts; service reads stay in logic hooks", () => {
  const view = read("../src/components/file/FileTree.tsx");
  const viewTypes = read("../src/components/file/fileTreeTypes.ts");
  const readiness = read("../src/components/file/useFileTreeProtectedAPI.ts");
  const accent = read("../src/components/file/useFileTreeProjectTabAccent.ts");
  const effects = read("../src/components/file/useFileTreeEffects.ts");
  const tabs = read("../src/components/file/FileTreeProjectTabs.tsx");
  const content = read("../src/components/file/FileTreeProjectContent.tsx");
  const entries = read("../src/components/file/fileTreeEntries.tsx");
  const palette = read("../src/shared/nodePalette.ts");
  const registry = read("../src/services/net/nodeRegistry.ts");
  const agentService = read("../src/services/agent/agents.ts");
  const agentConfigService = read("../src/services/agent/agentConfig.ts");
  const preferenceService = read("../src/services/prefs/preferences.ts");

  assert.doesNotMatch(view, /from "[^"\n]*services\//);
  assert.doesNotMatch(viewTypes, /from "[^"\n]*services\//);
  assert.doesNotMatch(view, /from "[^"\n]*app\//);
  assert.match(view, /useFileTreeProtectedAPI\(\)/);
  assert.match(view, /useFileTreeProjectTabAccent\(rootColor, entries, rootId\)/);
  assert.match(readiness, /bootstrapService\.subscribe/);
  assert.match(readiness, /bootstrapService\.canUseProtectedAPI/);
  assert.match(accent, /getNodes\(\)/);
  assert.match(entries, /from "\.\.\/\.\.\/shared\/nodePalette"/);
  assert.match(palette, /DEFAULT_NODE_COLOR = PALETTE\[0\]/);
  assert.match(registry, /export \{ DEFAULT_NODE_COLOR, LOCAL_NODE_ID, PALETTE \} from "\.\.\/\.\.\/shared\/nodePalette"/);
  assert.match(agentService, /export type \{[\s\S]*AgentStatus[\s\S]*\} from "\.\.\/\.\.\/shared\/agentTypes"/);
  assert.match(agentConfigService, /export type \{ AgentAPIProvider, AgentConfigBackup, AgentConfigSource \} from "\.\.\/\.\.\/shared\/agentTypes"/);
  assert.match(preferenceService, /export type \{ NewProjectMetaLocation \} from "\.\.\/\.\.\/shared\/preferencesTypes"/);
  assert.doesNotMatch(effects, /bootstrapService/);
  assert.doesNotMatch(tabs, /getNodes|DEFAULT_NODE_COLOR|services\//);
  assert.doesNotMatch(content, /services\//);
  assert.doesNotMatch(entries, /services\//);
  assert.match(tabs, /const tabAccent = accentColor/);
});
