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