import { test } from "vitest";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";
import { agentConfigHarnessName, isCodexProfileAgent, switchableAgentConfigBackups } from "../src/components/file/agentConfigProfiles";

const read = (path) => rawReadFileSync(new URL(path, import.meta.url), "utf8");

test("the agent menu keeps one config entry, one provider entry, and lifecycle", () => {
  const menu = read("../src/components/file/FileTreeAgentConfigMenuButtons.tsx");
  const en = read("../src/i18n/locales/en-US-projects.ts");
  const zh = read("../src/i18n/locales/zh-CN-projects.ts");

  assert.match(menu, /openAgentConfigFlow\("config"\)/);
  assert.match(menu, /openAgentConfigFlow\("provider"\)/);
  assert.match(menu, /openAgentLifecycleFlow/);
  assert.match(menu, /fileTree\.agentConfig/);
  assert.match(menu, /fileTree\.agentProviders/);
  assert.doesNotMatch(menu, /fileTree\.addAgentConfig|fileTree\.switchAgentConfig/);
  for (const locale of [en, zh]) {
    assert.match(locale, /"fileTree\.agentConfig"/);
    assert.match(locale, /"fileTree\.agentProviders"/);
  }
});

test("config details expose switch, backup, and sync tabs for every harness", () => {
  const popover = read("../src/components/file/AgentConfigPopover.tsx");
  const en = read("../src/i18n/locales/en-US-agents.ts");
  const zh = read("../src/i18n/locales/zh-CN-agents.ts");

  assert.match(popover, /export type AgentConfigFlow = "config" \| "provider"/);
  assert.match(popover, /export type AgentConfigDetailTab = "switch" \| "backup" \| "sync"/);
  assert.match(popover, /const tabs: AgentConfigDetailTab\[\] = \["switch", "backup", "sync"\]/);
  for (const key of ["tabSwitch", "tabBackup", "tabSync", "chooseConfigAgent", "chooseProviderAgent", "addProvider", "syncUnsupported", "continue"]) {
    assert.match(en, new RegExp(`agentConfig\\.${key}`));
    assert.match(zh, new RegExp(`agentConfig\\.${key}`));
  }
});

test("the provider entry only lists agents that can switch API providers", () => {
  const popover = read("../src/components/file/AgentConfigPopover.tsx");
  const selection = read("../src/components/file/useAgentConfigSelection.ts");

  assert.match(popover, /flow === "provider"[\s\S]*supports_api_provider_switch/);
  assert.match(selection, /agentConfigFlow === "provider"[\s\S]*fetchAgentAPIProviders/);
  assert.match(selection, /fetchAgentConfigSyncStatus\(\)/);
});

test("sync availability is derived from the backend harness registry", () => {
  const popover = read("../src/components/file/AgentConfigPopover.tsx");
  const profiles = read("../src/components/file/agentConfigProfiles.ts");
  const types = read("../src/shared/agentTypes.ts");
  const handler = read("../../server/internal/api/agent_config_sync_handlers.go");
  const result = read("../../internal/codexsync/types.go");

  assert.match(popover, /syncStatus\?\.harnesses/);
  assert.match(popover, /harness\.available/);
  assert.match(popover, /harness\.message/);
  assert.match(profiles, /agentConfigHarnessName/);
  assert.match(types, /harnesses\?: AgentConfigHarnessInfo\[\]/);
  assert.match(handler, /Harnesses = codexsync\.HarnessStatuses\(\)/);
  assert.match(result, /Harnesses\s+\[\]HarnessInfo/);
});

test("harness names normalize custom agent names before matching shared config", () => {
  assert.equal(agentConfigHarnessName("codex-custom", { protocol: "codex-sdk" }), "codex");
  assert.equal(agentConfigHarnessName("codex", { protocol: "" }), "codex");
  assert.equal(agentConfigHarnessName("claude-sdk", { protocol: "claude-sdk" }), "claude");
  assert.equal(agentConfigHarnessName("gemini", { protocol: "gemini" }), "gemini");
  assert.equal(isCodexProfileAgent("codex-custom", { protocol: "codex-sdk" }), true);
  assert.deepEqual(
    switchableAgentConfigBackups("claude", { protocol: "claude-sdk" }, [
      { id: "local", source: "local" },
      { id: "shared", source: "shared" },
    ]).map((item) => item.id),
    ["local", "shared"],
  );
});
test("the provider entry is named providers and models", () => {
  const zh = read("../src/i18n/locales/zh-CN-projects.ts");
  assert.match(zh, /"fileTree\.agentProviders":\s*"供应商和模型"/);
  assert.doesNotMatch(zh, /"fileTree\.agentProviders":\s*"API 供应商/);
});

test("agent config popovers close on outside clicks at every step", () => {
  const hook = read("../src/components/file/useAgentConfigOutsideClick.ts");
  const tree = read("../src/components/file/FileTree.tsx");

  assert.match(hook, /if \(!agentConfigFlow\)/);
  assert.doesNotMatch(hook, /agentConfigStep !== "agent"/);
  assert.match(hook, /document\.addEventListener\("mousedown", handlePointerDown\)/);
  assert.doesNotMatch(tree, /useAgentConfigOutsideClick\(\{[^}]*agentConfigStep/);
});

test("codex provider list follows the active shared profile", () => {
  const popover = read("../src/components/file/AgentConfigPopover.tsx");
  const scope = read("../../server/internal/api/agent_api_provider_scope.go");
  const handlers = read("../../server/internal/api/agent_api_provider_handlers.go");
  const switchOps = read("../../server/internal/api/agent_api_provider_sync_ops.go");
  const en = read("../src/i18n/locales/en-US-agents.ts");
  const zh = read("../src/i18n/locales/zh-CN-agents.ts");

  assert.match(scope, /SharedProfileForProviderAt/);
  assert.match(scope, /matched && profile != active/);
  assert.match(scope, /officialCodexAPIProvider/);
  assert.match(handlers, /scopeCodexAPIProvidersAt\(paths, providers, active\)/);
  assert.match(switchOps, /providerID == codexOfficialProviderID[\s\S]*shared:codex-default/);
  assert.match(popover, /item\.kind === "official"/);
  for (const locale of [en, zh]) {
    assert.match(locale, /agentConfig\.officialProvider/);
  }
});

test("custom models can be added and removed per harness", () => {
  const popover = read("../src/components/file/AgentConfigPopover.tsx");
  const models = read("../src/components/file/AgentConfigCustomModels.tsx");
  const overlay = read("../src/components/file/FileTreeAgentConfigOverlay.tsx");
  const selection = read("../src/components/file/useAgentConfigSelection.ts");
  const service = read("../src/services/agent/agentConfig.ts");
  const handler = read("../../server/internal/api/agent_custom_models.go");
  const routes = read("../../server/internal/api/http_routes.go");
  const store = read("../../server/internal/preferences/store.go");

  assert.match(popover, /<AgentConfigCustomModels/);
  assert.match(models, /agentConfig\.customModels/);
  assert.match(models, /models\.map/);
  assert.match(models, /onAdd\(input\.trim\(\)\)/);
  assert.match(models, /onRemove\(model\)/);
  assert.match(overlay, /agentCustomModelsState\.models/);
  assert.match(selection, /fetchAgentCustomModels\(agentName\)/);
  assert.match(selection, /saveAgentCustomModels\(agentConfigAgent/);
  assert.match(service, /\/api\/agents\/custom-models/);
  assert.match(handler, /handleAgentCustomModelsPut/);
  assert.match(handler, /BroadcastAgentStatusChanged/);
  assert.match(routes, /r\.Put\("\/api\/agents\/custom-models"/);
  assert.match(store, /CustomModels\s+\[\]string/);
  assert.match(store, /mergeCustomModels\(out\[i\]\.Models, defaults\.CustomModels\)/);
});
