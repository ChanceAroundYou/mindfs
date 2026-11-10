import { test } from "vitest";
import assert from "node:assert/strict";
import { rawReadFileSync } from "./source-map.mjs";

const read = (path) => rawReadFileSync(new URL(path, import.meta.url), "utf8");

test("FileTree delegates agent-catalog requests to its transition logic", () => {
  const view = read("../src/components/file/FileTree.tsx");
  const logic = read("../src/components/file/useAgentConfigFlowTransitions.ts");

  assert.doesNotMatch(view, /fetchAgentCatalog|fetchAgents/);
  assert.match(logic, /import \{ fetchAgentCatalog, fetchAgents, type AgentStatus \} from "\.\.\/\.\.\/services\/agents"/);
  assert.doesNotMatch(logic, /const \{[^}]*fetchAgentCatalog[^}]*\} = ctx/);
  assert.doesNotMatch(logic, /const \{[^}]*fetchAgents[^}]*\} = ctx/);
});

test("Codex config backups distinguish shared profiles from local recovery snapshots", () => {
  const popover = read("../src/components/file/AgentConfigPopover.tsx");
  const profiles = read("../src/components/file/AgentConfigProfileList.tsx");
  const selection = read("../src/components/file/useAgentConfigSelection.ts");
  const types = read("../src/shared/agentTypes.ts");

  assert.match(popover, /switchableAgentConfigBackups/);
  assert.match(popover, /<AgentConfigProfileList[\s\S]*backups=\{switchableBackups\}/);
  assert.match(profiles, /item\.source === "shared"/);
  assert.match(profiles, /!isShared[\s\S]*onDelete\(item\.id\)/);
  assert.match(profiles, /agentConfig\.sharedProfiles/);
  assert.match(profiles, /agentConfig\.localSnapshots/);
  assert.match(profiles, /item\.localCredentialAvailable === false/);
  assert.match(profiles, /agentConfig\.llmuxMissingKey/);
  assert.match(types, /source\?: "local" \| "shared"/);
  assert.match(selection, /switchableAgentConfigBackups/);
  assert.match(selection, /isCodexProfileAgent/);
  assert.match(selection, /last_config_selection/);
  assert.match(selection, /shared:codex-default/);
  assert.match(popover, /isCodexProfileAgent[\s\S]*effectiveSwitchTab === "api_provider"/);
});

test("failed local backup deletion reloads the server-owned backup list", () => {
  const actions = read("../src/components/file/useAgentConfigActions.ts");
  assert.match(actions, /catch \(error\)[\s\S]*fetchAgentConfigBackups\(agentConfigAgent\)/);
  assert.match(actions, /setAgentConfigBackups\(await fetchAgentConfigBackups\(agentConfigAgent\)\)/);
});

test("Codex config UI exposes safe sync status and missing local provider credentials", () => {
  const service = read("../src/services/agent/agentConfig.ts");
  const popover = read("../src/components/file/AgentConfigPopover.tsx");
  const status = read("../src/components/file/AgentConfigSyncStatus.tsx");
  const selection = read("../src/components/file/useAgentConfigSelection.ts");
  const en = read("../src/i18n/locales/en-US-agents.ts");
  const zh = read("../src/i18n/locales/zh-CN-agents.ts");

  assert.match(service, /fetchAgentConfigSyncStatus/);
  assert.match(service, /requestAgentConfigSync/);
  assert.match(popover, /item\.localCredentialAvailable === false/);
  assert.match(popover, /AgentConfigSyncStatus/);
  assert.match(status, /agentConfig\.retrySync/);
  assert.match(selection, /agentConfigSyncStatus\?\.state !== "syncing"/);
  assert.match(selection, /setTimeout\(poll, 1000\)/);
  const stateKeys = ["idle", "syncing", "synced", "pending", "conflict", "invalid", "unavailable"];
  for (const state of stateKeys) {
    assert.match(en, new RegExp(`agentConfig\\.syncState\\.${state}`));
    assert.match(zh, new RegExp(`agentConfig\\.syncState\\.${state}`));
  }
});
