import React from "react";
import { ProviderModelSelect } from "../agent/ProviderModelSelect";
import { useI18n } from "../../i18n/index";
import type { I18nContextValue } from "../../i18n/index";
import type { AgentAPIProvider, AgentConfigBackup, AgentConfigSyncResult, AgentStatus } from "../../shared/agentTypes";
import { TrashIcon } from "./icons";
import { AgentConfigLineEditor } from "./AgentConfigLineEditor";
import { AgentConfigAgentStep } from "./AgentConfigAgentStep";
import { AgentConfigProfileList } from "./AgentConfigProfileList";
import { AgentConfigSyncStatus } from "./AgentConfigSyncStatus";
import { AgentLifecyclePopover } from "./AgentLifecyclePopover";
import { agentConfigHarnessName, isCodexProfileAgent, switchableAgentConfigBackups } from "./agentConfigProfiles";
import {
  agentConfigActionRowStyle, agentConfigFieldStyle, agentConfigHintStyle,
  agentConfigIconButtonStyle, agentConfigInputStyle, agentConfigLabelStyle,
  agentConfigLineEditorStyle, agentConfigLineTextAreaStyle,
  agentConfigPrimaryButtonStyle, agentConfigSecondaryButtonStyle,
} from "./agentConfigStyles";
export { AgentLifecyclePopover } from "./AgentLifecyclePopover";
export { agentConfigActionRowStyle, agentConfigPrimaryButtonStyle, agentConfigSecondaryButtonStyle } from "./agentConfigStyles";

export type AgentConfigFlow = "config" | "provider";
export type AgentConfigStep = "agent" | "details" | "confirm";
// 「Agent 配置」流程里某个 harness 的详情页签；供应商/模型是独立流程，不在这里。
export type AgentConfigDetailTab = "switch" | "backup" | "sync";

function tabLabel(tab: AgentConfigDetailTab, t: I18nContextValue["t"]): string {
  if (tab === "switch") return t("agentConfig.tabSwitch");
  if (tab === "backup") return t("agentConfig.tabBackup");
  return t("agentConfig.tabSync");
}

function detailTabButtonStyle(active: boolean, disabled: boolean): React.CSSProperties {
  return {
    border: "1px solid var(--border-color)",
    background: active ? "var(--selection-bg)" : "transparent",
    color: active ? "var(--accent-color)" : "var(--text-primary)",
    borderRadius: "8px",
    padding: "7px 8px",
    fontSize: "12px",
    fontWeight: 700,
    cursor: disabled ? "default" : "pointer",
  };
}

export function AgentConfigPopover({
  flow,
  step,
  agents,
  selectedAgent,
  detailTab,
  showProviderForm,
  backupName,
  fileSourcesBody,
  envBody,
  apiProviderName,
  apiProviderBaseURL,
  apiProviderAPIKey,
  backups,
  apiProviders,
  selectedBackupID,
  selectedAPIProviderID,
  confirmMessage,
  busy,
  restartingAgent,
  error,
  notice,
  syncStatus,
  syncBusy,
  providerSyncBusy,
  providerTestBusyID,
  providerTestResults,
  onSyncAllProviders,
  onTestProviderModel,
  onProviderTestModelChange,
  providerTestModels,
  onChooseAgent,
  onDetailTabChange,
  onShowProviderFormChange,
  onBackupNameChange,
  onFileSourcesChange,
  onEnvBodyChange,
  onAPIProviderNameChange,
  onAPIProviderBaseURLChange,
  onAPIProviderAPIKeyChange,
  onSelectedBackupChange,
  onSelectedAPIProviderChange,
  onDeleteBackup,
  onDeleteAPIProvider,
  onSaveBackup,
  onSaveProvider,
  onSwitch,
  onRetrySync,
  onRestartAgent,
  onConfirm,
  onCancel,
}: {
  flow: AgentConfigFlow;
  step: AgentConfigStep;
  agents: AgentStatus[];
  selectedAgent: string;
  detailTab: AgentConfigDetailTab;
  showProviderForm: boolean;
  backupName: string;
  fileSourcesBody: string;
  envBody: string;
  apiProviderName: string;
  apiProviderBaseURL: string;
  apiProviderAPIKey: string;
  backups: AgentConfigBackup[];
  apiProviders: AgentAPIProvider[];
  selectedBackupID: string;
  selectedAPIProviderID: string;
  confirmMessage: string;
  busy: boolean;
  restartingAgent: string;
  error: string;
  notice: string;
  syncStatus: AgentConfigSyncResult | null;
  syncBusy: boolean;
  providerSyncBusy: boolean;
  providerTestBusyID: string;
  providerTestResults: Record<string, string>;
  onSyncAllProviders: () => void;
  onTestProviderModel: (providerID: string, model: string) => void;
  onProviderTestModelChange: (providerID: string, model: string) => void;
  providerTestModels: Record<string, string>;
  onChooseAgent: (name: string) => void;
  onDetailTabChange: (tab: AgentConfigDetailTab) => void;
  onShowProviderFormChange: (show: boolean) => void;
  onBackupNameChange: (value: string) => void;
  onFileSourcesChange: (value: string) => void;
  onEnvBodyChange: (value: string) => void;
  onAPIProviderNameChange: (value: string) => void;
  onAPIProviderBaseURLChange: (value: string) => void;
  onAPIProviderAPIKeyChange: (value: string) => void;
  onSelectedBackupChange: (value: string) => void;
  onSelectedAPIProviderChange: (value: string) => void;
  onDeleteBackup: (id: string) => void;
  onDeleteAPIProvider: (id: string) => void;
  onSaveBackup: () => void;
  onSaveProvider: () => void;
  onSwitch: () => void;
  onRetrySync: () => void;
  onRestartAgent?: (agentName: string) => void | Promise<void>;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const { t } = useI18n();
  const agentTitle = flow === "config"
    ? t("agentConfig.chooseConfigAgent")
    : t("agentConfig.chooseProviderAgent");
  const selectedAgentStatus = agents.find((item) => item.name === selectedAgent);
  const isCodex = isCodexProfileAgent(selectedAgent, selectedAgentStatus);
  const switchableBackups = switchableAgentConfigBackups(selectedAgent, selectedAgentStatus, backups);
  const selectedBackupIsSwitchable = switchableBackups.some((item) => item.id === selectedBackupID);
  const visibleAgents = flow === "provider"
    ? agents.filter((item) => item.supports_api_provider_switch)
    : agents;
  const harnessName = agentConfigHarnessName(selectedAgent, selectedAgentStatus);
  const harness = (syncStatus?.harnesses || []).find((item) => item.name === harnessName);
  // 旧版 worker 不返回 harnesses：Codex 仍按改造前行为显示同步状态，
  // 其他 harness 明说没有共享同步，不伪造入口。
  const harnessRegistryKnown = Array.isArray(syncStatus?.harnesses);
  const syncSupported = harness ? harness.available : !harnessRegistryKnown && isCodex;

  const renderConfigDetails = () => {
    const tabs: AgentConfigDetailTab[] = ["switch", "backup", "sync"];
    return (
      <>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr 1fr", gap: "6px" }}>
          {tabs.map((tab) => {
            const active = detailTab === tab;
            return (
              <button
                key={tab}
                type="button"
                disabled={busy}
                onClick={() => onDetailTabChange(tab)}
                style={detailTabButtonStyle(active, busy)}
              >
                {tabLabel(tab, t)}
              </button>
            );
          })}
        </div>
        {detailTab === "switch" ? (
          <>
            <div style={{ display: "flex", flexDirection: "column", gap: "8px", maxHeight: "260px", overflow: "auto" }}>
              {busy ? (
                <div style={agentConfigHintStyle}>{t("agentConfig.loading")}</div>
              ) : switchableBackups.length === 0 ? (
                <div style={agentConfigHintStyle}>{t("agentConfig.noSwitchableConfig")}</div>
              ) : (
                <AgentConfigProfileList
                  backups={switchableBackups}
                  selectedID={selectedBackupID}
                  busy={busy}
                  onSelect={onSelectedBackupChange}
                  onDelete={onDeleteBackup}
                />
              )}
            </div>
            <div style={agentConfigActionRowStyle}>
              <button type="button" disabled={busy} onClick={onCancel} style={agentConfigSecondaryButtonStyle(busy)}>
                {t("common.cancel")}
              </button>
              <button
                type="button"
                disabled={busy || !selectedBackupIsSwitchable}
                onClick={onSwitch}
                style={agentConfigPrimaryButtonStyle(busy || !selectedBackupIsSwitchable)}
              >
                {t("agentConfig.switch")}
              </button>
            </div>
          </>
        ) : detailTab === "backup" ? (
          <>
            <div style={agentConfigFieldStyle}>
              <label style={agentConfigLabelStyle}>{t("agentConfig.backupName")}</label>
              <input
                value={backupName}
                onChange={(event) => onBackupNameChange(event.target.value)}
                placeholder="work"
                style={agentConfigInputStyle}
              />
            </div>
            <div style={agentConfigFieldStyle}>
              <label style={agentConfigLabelStyle}>{t("agentConfig.fileSources")}</label>
              <AgentConfigLineEditor
                value={fileSourcesBody}
                onChange={onFileSourcesChange}
                placeholder={t("agentConfig.fileSourcePlaceholder")}
              />
            </div>
            <div style={agentConfigFieldStyle}>
              <label style={agentConfigLabelStyle}>{t("agentConfig.env")}</label>
              <AgentConfigLineEditor
                value={envBody}
                onChange={onEnvBodyChange}
                placeholder={t("agentConfig.envPlaceholder")}
              />
            </div>
            <div style={agentConfigActionRowStyle}>
              <button type="button" disabled={busy} onClick={onCancel} style={agentConfigSecondaryButtonStyle(busy)}>
                {t("common.cancel")}
              </button>
              <button type="button" disabled={busy} onClick={onSaveBackup} style={agentConfigPrimaryButtonStyle(busy)}>
                {t("common.save")}
              </button>
            </div>
          </>
        ) : (
          <>
            {!syncSupported ? (
              <div style={{ ...agentConfigHintStyle, border: "1px solid var(--border-color)", borderRadius: "8px", padding: "8px" }}>
                <div style={{ fontWeight: 700 }}>{t("agentConfig.syncUnsupported", { agent: selectedAgent })}</div>
                {harness?.message ? <div style={{ marginTop: "3px", overflowWrap: "anywhere" }}>{harness.message}</div> : null}
              </div>
            ) : (
              <AgentConfigSyncStatus status={syncStatus} busy={syncBusy} onRetry={onRetrySync} />
            )}
            <div style={agentConfigActionRowStyle}>
              <button type="button" disabled={busy} onClick={onCancel} style={agentConfigSecondaryButtonStyle(busy)}>
                {t("common.cancel")}
              </button>
            </div>
          </>
        )}
      </>
    );
  };

  const renderProviderDetails = () => (
    <>
      <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
        <button
          type="button"
          disabled={busy || providerSyncBusy || apiProviders.length === 0}
          onClick={onSyncAllProviders}
          style={{
            border: "1px solid var(--border-color)",
            background: "transparent",
            color: providerSyncBusy || apiProviders.length === 0 ? "var(--text-secondary)" : "var(--accent-color)",
            borderRadius: "8px",
            padding: "5px 10px",
            fontSize: "11px",
            fontWeight: 600,
            cursor: providerSyncBusy || apiProviders.length === 0 ? "default" : "pointer",
          }}
        >
          {providerSyncBusy ? t("agentConfig.syncingProviders") : t("agentConfig.syncAllProviders")}
        </button>
        <button
          type="button"
          disabled={busy}
          onClick={() => onShowProviderFormChange(!showProviderForm)}
          style={{
            border: "1px solid var(--border-color)",
            background: showProviderForm ? "var(--selection-bg)" : "transparent",
            color: showProviderForm ? "var(--accent-color)" : "var(--text-primary)",
            borderRadius: "8px",
            padding: "5px 10px",
            fontSize: "11px",
            fontWeight: 600,
            cursor: busy ? "default" : "pointer",
          }}
        >
          {t("agentConfig.addProvider")}
        </button>
        <span style={{ fontSize: "11px", color: "var(--text-secondary)", minWidth: 0, flex: 1 }}>
          {t("agentConfig.syncAllProvidersHint")}
        </span>
      </div>
      {showProviderForm ? (
        <>
          <div style={agentConfigFieldStyle}>
            <label style={agentConfigLabelStyle}>{t("agentConfig.providerName")}</label>
            <input
              value={apiProviderName}
              onChange={(event) => onAPIProviderNameChange(event.target.value)}
              style={agentConfigInputStyle}
            />
          </div>
          <div style={agentConfigFieldStyle}>
            <label style={agentConfigLabelStyle}>Base URL</label>
            <input
              value={apiProviderBaseURL}
              onChange={(event) => onAPIProviderBaseURLChange(event.target.value)}
              style={agentConfigInputStyle}
            />
          </div>
          <div style={agentConfigFieldStyle}>
            <label style={agentConfigLabelStyle}>API Key</label>
            <input
              value={apiProviderAPIKey}
              type="password"
              onChange={(event) => onAPIProviderAPIKeyChange(event.target.value)}
              placeholder="sk-..."
              style={agentConfigInputStyle}
            />
          </div>
          <div style={agentConfigActionRowStyle}>
            <button type="button" disabled={busy} onClick={() => onShowProviderFormChange(false)} style={agentConfigSecondaryButtonStyle(busy)}>
              {t("common.cancel")}
            </button>
            <button type="button" disabled={busy} onClick={onSaveProvider} style={agentConfigPrimaryButtonStyle(busy)}>
              {t("agentConfig.validateAndSave")}
            </button>
          </div>
        </>
      ) : null}
      <div style={{ display: "flex", flexDirection: "column", gap: "8px", maxHeight: "260px", overflow: "auto" }}>
        {busy ? (
          <div style={agentConfigHintStyle}>{t("agentConfig.loading")}</div>
        ) : apiProviders.length === 0 ? (
          <div style={agentConfigHintStyle}>{t("agentConfig.noAPIProviders")}</div>
        ) : apiProviders.map((item) => {
          const selected = item.id === selectedAPIProviderID;
          const testBusy = providerTestBusyID === item.id;
          const testResult = providerTestResults[item.id] || "";
          const models = item.models || [];
          return (
            <div
              key={item.id}
              onClick={() => onSelectedAPIProviderChange(item.id)}
              style={{
                border: "1px solid var(--border-color)",
                background: selected ? "var(--selection-bg)" : "transparent",
                color: selected ? "var(--accent-color)" : "var(--text-primary)",
                borderRadius: "8px",
                padding: "6px 8px",
                textAlign: "left",
                cursor: "pointer",
              }}
            >
              <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
                <div title={item.name} style={{ minWidth: 0, flex: 1, fontSize: "12px", fontWeight: 600, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{item.name}</div>
                <button
                  type="button"
                  aria-label={t("agentConfig.deleteAPIProvider", { name: item.name })}
                  title={t("common.delete")}
                  disabled={busy}
                  onClick={(event) => {
                    event.stopPropagation();
                    onDeleteAPIProvider(item.id);
                  }}
                  style={agentConfigIconButtonStyle(busy)}
                >
                  <TrashIcon size={18} />
                </button>
              </div>
              {item.localCredentialAvailable === false ? (
                <div style={{ marginTop: "3px", fontSize: "10px", color: "var(--text-secondary)" }}>{t("agentConfig.providerMissingKey")}</div>
              ) : null}
              {models.length > 0 ? (
                <div style={{ display: "flex", alignItems: "center", gap: "8px", marginTop: "4px", minWidth: 0 }}>
                  <ProviderModelSelect
                    models={models}
                    value={providerTestModels[item.id] || models[0] || ""}
                    label={t("agentConfig.testModel", { name: item.name })}
                    onChange={(model) => onProviderTestModelChange(item.id, model)}
                  />
                  <button
                    type="button"
                    aria-label={t("agentConfig.testModel", { name: item.name })}
                    title={t("agentConfig.testModel", { name: item.name })}
                    disabled={busy || testBusy || item.localCredentialAvailable === false}
                    onClick={(event) => {
                      event.stopPropagation();
                      onTestProviderModel(item.id, providerTestModels[item.id] || models[0] || "");
                    }}
                    style={{ ...agentConfigSecondaryButtonStyle(busy || testBusy || item.localCredentialAvailable === false), flex: "0 0 auto", height: "22px", minHeight: 0, boxSizing: "border-box", padding: "0 8px", lineHeight: "18px", fontSize: "11px", whiteSpace: "nowrap" }}
                  >
                    {testBusy ? "…" : t("agentConfig.testModelShort")}
                  </button>
                </div>
              ) : null}
              {testResult ? (
                <div style={{ marginTop: "4px", fontSize: "11px", color: "var(--text-secondary)", whiteSpace: "normal", overflowWrap: "anywhere", wordBreak: "break-word" }}>
                  {testResult}
                </div>
              ) : null}
            </div>
          );
        })}
      </div>
      <div style={agentConfigActionRowStyle}>
        <button type="button" disabled={busy} onClick={onCancel} style={agentConfigSecondaryButtonStyle(busy)}>
          {t("common.cancel")}
        </button>
        <button
          type="button"
          disabled={busy || !selectedAPIProviderID}
          onClick={onSwitch}
          style={agentConfigPrimaryButtonStyle(busy || !selectedAPIProviderID)}
        >
          {t("agentConfig.switch")}
        </button>
      </div>
    </>
  );

  return (
    <div
      style={{
        width: "100%",
        padding: "10px",
        borderRadius: "12px",
        border: "1px solid var(--border-color)",
        background: "var(--menu-bg)",
        boxShadow: "0 12px 30px rgba(15, 23, 42, 0.14)",
        display: "flex",
        flexDirection: "column",
        gap: "10px",
      }}
    >
      {step === "agent" ? (
        <AgentConfigAgentStep
          title={agentTitle}
          agents={visibleAgents}
          busy={busy}
          selectedAgent={selectedAgent}
          restartingAgent={restartingAgent}
          onRestartAgent={onRestartAgent}
          onChooseAgent={onChooseAgent}
        />
      ) : step === "confirm" ? (
        <>
          <div style={{ ...agentConfigHintStyle, color: "#dc2626" }}>
            {confirmMessage || t("agentConfig.targetExists")}
          </div>
          <div style={agentConfigActionRowStyle}>
            <button type="button" disabled={busy} onClick={onCancel} style={agentConfigSecondaryButtonStyle(busy)}>
              {t("common.cancel")}
            </button>
            <button type="button" disabled={busy} onClick={onConfirm} style={agentConfigPrimaryButtonStyle(busy)}>
              {t("agentConfig.continue")}
            </button>
          </div>
        </>
      ) : flow === "config" ? (
        renderConfigDetails()
      ) : (
        renderProviderDetails()
      )}
      {notice ? <div style={{ ...agentConfigHintStyle, whiteSpace: "pre-line" }}>{notice}</div> : null}
      {error ? <div style={{ ...agentConfigHintStyle, color: "#dc2626" }}>{error}</div> : null}
    </div>
  );
}