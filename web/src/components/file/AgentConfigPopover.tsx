import React from "react";
import { ProviderModelSelect } from "../agent/ProviderModelSelect";
import { useI18n } from "../../i18n/index";
import type { AgentAPIProvider, AgentConfigBackup, AgentStatus } from "../../shared/agentTypes";
import { TrashIcon } from "./icons";
import { AgentConfigLineEditor } from "./AgentConfigLineEditor";
import { AgentConfigAgentStep } from "./AgentConfigAgentStep";
import { AgentLifecyclePopover } from "./AgentLifecyclePopover";
import {
  agentConfigActionRowStyle, agentConfigFieldStyle, agentConfigHintStyle,
  agentConfigIconButtonStyle, agentConfigInputStyle, agentConfigLabelStyle,
  agentConfigLineEditorStyle, agentConfigLineTextAreaStyle,
  agentConfigPrimaryButtonStyle, agentConfigSecondaryButtonStyle,
} from "./agentConfigStyles";
export { AgentLifecyclePopover } from "./AgentLifecyclePopover";
export { agentConfigActionRowStyle, agentConfigPrimaryButtonStyle, agentConfigSecondaryButtonStyle } from "./agentConfigStyles";

export type AgentConfigFlow = "backup" | "switch";
export type AgentConfigStep = "agent" | "details" | "confirm";
export type AgentConfigAddTab = "backup" | "api";
export type AgentConfigSwitchTab = "backup" | "api_provider";

export function AgentConfigPopover({
  flow,
  step,
  agents,
  selectedAgent,
  addTab,
  switchTab,
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
  providerSyncBusy,
  providerTestBusyID,
  providerTestResults,
  onSyncAllProviders,
  onTestProviderModel,
  onProviderTestModelChange,
  providerTestModels,
  onChooseAgent,
  onAddTabChange,
  onSwitchTabChange,
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
  onSave,
  onSwitch,
  onRestartAgent,
  onConfirm,
  onCancel,
}: {
  flow: AgentConfigFlow;
  step: AgentConfigStep;
  agents: AgentStatus[];
  selectedAgent: string;
  addTab: AgentConfigAddTab;
  switchTab: AgentConfigSwitchTab;
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
  providerSyncBusy: boolean;
  providerTestBusyID: string;
  providerTestResults: Record<string, string>;
  onSyncAllProviders: () => void;
  onTestProviderModel: (providerID: string, model: string) => void;
  onProviderTestModelChange: (providerID: string, model: string) => void;
  providerTestModels: Record<string, string>;
  onChooseAgent: (name: string) => void;
  onAddTabChange: (tab: AgentConfigAddTab) => void;
  onSwitchTabChange: (tab: AgentConfigSwitchTab) => void;
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
  onSave: () => void;
  onSwitch: () => void;
  onRestartAgent?: (agentName: string) => void | Promise<void>;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const { t } = useI18n();
  const agentTitle = flow === "backup"
    ? t("agentConfig.chooseBackupAgent")
    : t("agentConfig.chooseSwitchAgent");
  const confirmButtonLabel = flow === "backup" ? t("agentConfig.continueBackup") : t("agentConfig.continueSwitch");
  const selectedAgentStatus = agents.find((item) => item.name === selectedAgent);
  const supportsAPIProvider = Boolean(selectedAgentStatus?.supports_api_provider_switch);
  const addTabs: AgentConfigAddTab[] = supportsAPIProvider ? ["backup", "api"] : ["backup"];
  const switchTabs: AgentConfigSwitchTab[] = supportsAPIProvider ? ["backup", "api_provider"] : ["backup"];
  const effectiveAddTab: AgentConfigAddTab = supportsAPIProvider ? addTab : "backup";
  const effectiveSwitchTab: AgentConfigSwitchTab = supportsAPIProvider ? switchTab : "backup";
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
          flow={flow}
          agents={agents}
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
              {confirmButtonLabel}
            </button>
          </div>
        </>
      ) : flow === "backup" ? (
        <>
          {addTabs.length > 1 ? (
            <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "6px" }}>
              {addTabs.map((tab) => {
                const active = effectiveAddTab === tab;
                return (
                  <button
                    key={tab}
                    type="button"
                    disabled={busy}
                    onClick={() => onAddTabChange(tab)}
                    style={{
                      border: "1px solid var(--border-color)",
                      background: active ? "var(--selection-bg)" : "transparent",
                      color: active ? "var(--accent-color)" : "var(--text-primary)",
                      borderRadius: "8px",
                      padding: "7px 8px",
                      fontSize: "12px",
                      fontWeight: 700,
                      cursor: busy ? "default" : "pointer",
                    }}
                  >
                    {tab === "backup" ? t("agentConfig.backup") : t("agentConfig.apiProvider")}
                  </button>
                );
              })}
            </div>
          ) : null}
          {effectiveAddTab === "backup" ? (
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
            </>
          ) : (
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
            </>
          )}
          <div style={agentConfigActionRowStyle}>
            <button type="button" disabled={busy} onClick={onCancel} style={agentConfigSecondaryButtonStyle(busy)}>
              {t("common.cancel")}
            </button>
            <button type="button" disabled={busy} onClick={onSave} style={agentConfigPrimaryButtonStyle(busy)}>
              {effectiveAddTab === "backup" ? t("common.save") : t("agentConfig.validateAndSave")}
            </button>
          </div>
        </>
      ) : (
        <>
          {switchTabs.length > 1 ? (
            <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "6px" }}>
              {switchTabs.map((tab) => {
                const active = effectiveSwitchTab === tab;
                return (
                  <button
                    key={tab}
                    type="button"
                    disabled={busy}
                    onClick={() => onSwitchTabChange(tab)}
                    style={{
                      border: "1px solid var(--border-color)",
                      background: active ? "var(--selection-bg)" : "transparent",
                      color: active ? "var(--accent-color)" : "var(--text-primary)",
                      borderRadius: "8px",
                      padding: "7px 8px",
                      fontSize: "12px",
                      fontWeight: 700,
                      cursor: busy ? "default" : "pointer",
                    }}
                  >
                    {tab === "backup" ? t("agentConfig.backup") : t("agentConfig.apiProvider")}
                  </button>
                );
              })}
            </div>
          ) : null}
          {effectiveSwitchTab === "api_provider" ? (
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
              <span style={{ fontSize: "11px", color: "var(--text-secondary)", minWidth: 0, flex: 1 }}>
                {t("agentConfig.syncAllProvidersHint")}
              </span>
            </div>
          ) : null}
          <div style={{ display: "flex", flexDirection: "column", gap: "8px", maxHeight: "260px", overflow: "auto" }}>
            {busy ? (
              <div style={agentConfigHintStyle}>{t("agentConfig.loading")}</div>
            ) : backups.length === 0 && (supportsAPIProvider ? apiProviders.length === 0 : true) ? (
              <div style={agentConfigHintStyle}>{t("agentConfig.noSwitchableConfig")}</div>
            ) : effectiveSwitchTab === "backup" ? (
              backups.length === 0 ? (
                <div style={agentConfigHintStyle}>{t("agentConfig.noBackups")}</div>
              ) : backups.map((item) => {
                const selected = item.id === selectedBackupID;
                return (
                  <div
                    key={item.id}
                    onClick={() => onSelectedBackupChange(item.id)}
                    style={{
                      border: "1px solid var(--border-color)",
                      background: selected ? "var(--selection-bg)" : "transparent",
                      color: selected ? "var(--accent-color)" : "var(--text-primary)",
                      borderRadius: "8px",
                      padding: "8px 10px",
                      textAlign: "left",
                      cursor: "pointer",
                    }}
                  >
                    <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
                      <div style={{ minWidth: 0, flex: 1, fontSize: "12px", fontWeight: 600, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{item.name}</div>
                      <button
                        type="button"
                        aria-label={t("agentConfig.deleteConfig", { name: item.name })}
                        title={t("common.delete")}
                        disabled={busy}
                        onClick={(event) => {
                          event.stopPropagation();
                          onDeleteBackup(item.id);
                        }}
                        style={agentConfigIconButtonStyle(busy)}
                      >
                        <TrashIcon />
                      </button>
                    </div>
                  </div>
                );
              })
            ) : (
	              apiProviders.length === 0 ? (
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
                          disabled={busy || testBusy}
                          onClick={(event) => {
                            event.stopPropagation();
                            onTestProviderModel(item.id, providerTestModels[item.id] || models[0] || "");
                          }}
                          style={{ ...agentConfigSecondaryButtonStyle(busy || testBusy), flex: "0 0 auto", height: "22px", minHeight: 0, boxSizing: "border-box", padding: "0 8px", lineHeight: "18px", fontSize: "11px", whiteSpace: "nowrap" }}
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
              })
            )}
          </div>
          <div style={agentConfigActionRowStyle}>
            <button type="button" disabled={busy} onClick={onCancel} style={agentConfigSecondaryButtonStyle(busy)}>
              {t("common.cancel")}
            </button>
            <button
              type="button"
              disabled={busy || (effectiveSwitchTab === "backup" ? !selectedBackupID : !selectedAPIProviderID)}
              onClick={onSwitch}
              style={agentConfigPrimaryButtonStyle(busy || (effectiveSwitchTab === "backup" ? !selectedBackupID : !selectedAPIProviderID))}
            >
              {t("agentConfig.switch")}
            </button>
          </div>
        </>
      )}
      {notice ? <div style={{ ...agentConfigHintStyle, whiteSpace: "pre-line" }}>{notice}</div> : null}
      {error ? <div style={{ ...agentConfigHintStyle, color: "#dc2626" }}>{error}</div> : null}
    </div>
  );
}
