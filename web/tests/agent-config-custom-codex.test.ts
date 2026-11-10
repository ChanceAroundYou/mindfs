import assert from "node:assert/strict";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, it } from "vitest";
import { AgentConfigPopover } from "../src/components/file/AgentConfigPopover";
import { I18nProvider, translateNow } from "../src/i18n";

const profileUtilsPromise = import("../src/components/file/agentConfigProfiles").catch(() => null);

const noop = () => undefined;

function renderCustomCodexPopover() {
  return renderToStaticMarkup(React.createElement(
      I18nProvider,
      null,
      React.createElement(AgentConfigPopover, {
        flow: "switch",
        step: "details",
        agents: [{
          name: "codex-custom",
          protocol: "codex-sdk",
          installed: true,
          available: true,
          supports_api_provider_switch: true,
        }],
        selectedAgent: "codex-custom",
        addTab: "backup",
        switchTab: "backup",
        backupName: "",
        fileSourcesBody: "",
        envBody: "",
        apiProviderName: "",
        apiProviderBaseURL: "",
        apiProviderAPIKey: "",
        backups: [{
          id: "codex-custom-recovery",
          agent: "codex-custom",
          name: "custom-private-recovery-snapshot",
          source: "local",
        }],
        apiProviders: [],
        selectedBackupID: "codex-custom-recovery",
        selectedAPIProviderID: "",
        confirmMessage: "",
        busy: false,
        restartingAgent: "",
        error: "",
        notice: "",
        syncStatus: null,
        syncBusy: false,
        providerSyncBusy: false,
        providerTestBusyID: "",
        providerTestResults: {},
        providerTestModels: {},
        onSyncAllProviders: noop,
        onTestProviderModel: noop,
        onProviderTestModelChange: noop,
        onChooseAgent: noop,
        onAddTabChange: noop,
        onSwitchTabChange: noop,
        onBackupNameChange: noop,
        onFileSourcesChange: noop,
        onEnvBodyChange: noop,
        onAPIProviderNameChange: noop,
        onAPIProviderBaseURLChange: noop,
        onAPIProviderAPIKeyChange: noop,
        onSelectedBackupChange: noop,
        onSelectedAPIProviderChange: noop,
        onDeleteBackup: noop,
        onDeleteAPIProvider: noop,
        onSave: noop,
        onSwitch: noop,
        onRetrySync: noop,
        onConfirm: noop,
        onCancel: noop,
      }),
  ));
}

describe("agent config for custom-named Codex SDK agents", () => {
  it("recognizes Codex by protocol and filters local recovery snapshots", async () => {
    const profileUtils = await profileUtilsPromise as null | {
      isCodexProfileAgent?: (name: string, agent?: { protocol?: string }) => boolean;
      switchableAgentConfigBackups?: (name: string, agent: { protocol?: string }, backups: Array<{ id: string; source?: string }>) => Array<{ id: string; source?: string }>;
    };
    assert.equal(typeof profileUtils?.isCodexProfileAgent, "function");
    assert.equal(profileUtils?.isCodexProfileAgent?.("codex-custom", { protocol: "codex-sdk" }), true);
    assert.equal(profileUtils?.isCodexProfileAgent?.("codex", { protocol: "acp" }), false);
    // 旧版 worker 的状态可能没有 protocol，按名字兜底识别 Codex。
    assert.equal(profileUtils?.isCodexProfileAgent?.("codex", { protocol: "" }), true);
    assert.equal(profileUtils?.isCodexProfileAgent?.("codex-custom", { protocol: "" }), false);
    assert.deepEqual(
      profileUtils?.switchableAgentConfigBackups?.("codex-custom", { protocol: "codex-sdk" }, [
        { id: "local", source: "local" },
        { id: "shared:codex-default", source: "shared" },
      ]),
      [{ id: "shared:codex-default", source: "shared" }],
    );
  });

  it("does not render local recovery snapshots as switch targets", () => {
    const markup = renderCustomCodexPopover();
    assert.equal(markup.includes("custom-private-recovery-snapshot"), false);
    assert.equal(markup.includes('disabled="" style='), true);
  });

  it("does not render the API-provider switch tab", () => {
    const markup = renderCustomCodexPopover();
    assert.equal(markup.includes(translateNow("agentConfig.apiProvider")), false);
  });
});
