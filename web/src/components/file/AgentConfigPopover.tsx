import React from "react";
import { ProviderModelSelect } from "../agent/ProviderModelSelect";
import { AgentMenuList } from "../agent/AgentMenuList";
import { AgentIcon } from "../agent/AgentIcon";
import { useI18n } from "../../i18n/index";
import { type AgentStatus } from "../../services/agents";
import { type AgentAPIProvider, type AgentConfigBackup } from "../../services/agentConfig";
import { RestartSpinner, TrashIcon } from "./icons";

export type AgentConfigFlow = "backup" | "switch";
export type AgentConfigStep = "agent" | "details" | "confirm";
export type AgentConfigAddTab = "backup" | "api";
export type AgentConfigSwitchTab = "backup" | "api_provider";

export function AgentConfigLineEditor({
  value,
  onChange,
  placeholder,
}: {
  value: string;
  onChange: (value: string) => void;
  placeholder: string;
}) {
  const refs = React.useRef<Array<HTMLTextAreaElement | null>>([]);
  const lines = value.length > 0 ? value.split("\n") : [""];

  const emitLines = React.useCallback((nextLines: string[]) => {
    onChange(nextLines.join("\n"));
  }, [onChange]);

  React.useLayoutEffect(() => {
    for (const node of refs.current) {
      if (!node) {
        continue;
      }
      node.style.height = "auto";
      node.style.height = `${node.scrollHeight}px`;
    }
  }, [lines]);

  return (
    <div style={agentConfigLineEditorStyle}>
      {lines.map((line, index) => (
        <textarea
          key={index}
          ref={(node) => {
            refs.current[index] = node;
          }}
          value={line}
          onChange={(event) => {
            const nextValue = event.target.value;
            const nextLines = [...lines];
            if (nextValue.includes("\n")) {
              nextLines.splice(index, 1, ...nextValue.split(/\r?\n/));
            } else {
              nextLines[index] = nextValue;
            }
            emitLines(nextLines);
          }}
          onKeyDown={(event) => {
            const target = event.currentTarget;
            if (event.key === "Enter") {
              event.preventDefault();
              const before = line.slice(0, target.selectionStart);
              const after = line.slice(target.selectionEnd);
              const nextLines = [...lines];
              nextLines.splice(index, 1, before, after);
              emitLines(nextLines);
              window.setTimeout(() => refs.current[index + 1]?.focus(), 0);
              return;
            }
            if (event.key === "Backspace" && target.selectionStart === 0 && target.selectionEnd === 0 && index > 0) {
              event.preventDefault();
              const previous = lines[index - 1] || "";
              const nextLines = [...lines];
              nextLines.splice(index - 1, 2, previous + line);
              emitLines(nextLines);
              window.setTimeout(() => {
                const previousNode = refs.current[index - 1];
                previousNode?.focus();
                previousNode?.setSelectionRange(previous.length, previous.length);
              }, 0);
              return;
            }
            if (event.key === "Delete" && target.selectionStart === line.length && target.selectionEnd === line.length && index < lines.length - 1) {
              event.preventDefault();
              const nextLines = [...lines];
              nextLines.splice(index, 2, line + (lines[index + 1] || ""));
              emitLines(nextLines);
              window.setTimeout(() => {
                const node = refs.current[index];
                node?.focus();
                node?.setSelectionRange(line.length, line.length);
              }, 0);
            }
          }}
          placeholder={lines.length === 1 && !line ? placeholder : ""}
          rows={1}
          style={agentConfigLineTextAreaStyle}
        />
      ))}
    </div>
  );
}

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
        <div style={{ fontSize: "12px", fontWeight: 700, color: "var(--text-primary)" }}>
          {agentTitle}
        </div>
      ) : null}
      {step === "agent" ? (
        <>
          {busy ? (
            <div style={agentConfigHintStyle}>{t("agentConfig.loading")}</div>
          ) : agents.length === 0 ? (
            <div style={agentConfigHintStyle}>{t("agentConfig.noInstalledAgents")}</div>
          ) : (
            <AgentMenuList
              agents={agents}
              selectedAgent={selectedAgent}
              maxHeight="220px"
              renderEnd={(agent) => {
                const name = String(agent.last_config_selection?.name || "").trim();
                const restarting = agent.name === restartingAgent;
                if (flow !== "switch" || !onRestartAgent) {
                  if (!name) {
                    return null;
                  }
                  return (
                    <span
                      title={t("agentConfig.lastSelected", { name })}
                      style={{
                        maxWidth: "120px",
                        minWidth: 0,
                        flexShrink: 1,
                        overflow: "hidden",
                        textOverflow: "ellipsis",
                        whiteSpace: "nowrap",
                        fontSize: "11px",
                        color: agent.name === selectedAgent ? "var(--accent-color)" : "var(--text-secondary)",
                      }}
                    >
                      {name}
                    </span>
                  );
                }
                return (
                  <>
                    {name ? (
                      <span
                        title={t("agentConfig.lastSelected", { name })}
                        style={{
                          maxWidth: "86px",
                          minWidth: 0,
                          flexShrink: 1,
                          overflow: "hidden",
                          textOverflow: "ellipsis",
                          whiteSpace: "nowrap",
                          fontSize: "11px",
                          color: agent.name === selectedAgent ? "var(--accent-color)" : "var(--text-secondary)",
                        }}
                      >
                        {name}
                      </span>
                    ) : null}
                    <button
                      type="button"
                      disabled={busy || restartingAgent !== ""}
                      onClick={(event) => {
                        event.stopPropagation();
                        void onRestartAgent(agent.name);
                      }}
                      style={{
                        ...agentConfigSecondaryButtonStyle(busy || restartingAgent !== ""),
                        padding: "2px 6px",
                        minWidth: "36px",
                        height: "18px",
                        lineHeight: "12px",
                        fontSize: "11px",
                        borderRadius: "5px",
                        flexShrink: 0,
                      }}
                    >
                      {restarting ? <RestartSpinner /> : t("agentConfig.restart")}
                    </button>
                  </>
                );
              }}
              onSelect={onChooseAgent}
            />
          )}
        </>
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

export function AgentLifecyclePopover({
  agents,
  busy,
  runningAgent,
  error,
  onRun,
}: {
  agents: AgentStatus[];
  busy: boolean;
  runningAgent: string;
  error: string;
  onRun: (agent: AgentStatus, action: "install" | "update") => void;
}) {
  const { t } = useI18n();
  const [expandedDescriptions, setExpandedDescriptions] = React.useState<Set<string>>(() => new Set());

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
      <div style={{ fontSize: "12px", fontWeight: 700, color: "var(--text-primary)" }}>
        {t("agentConfig.lifecycleTitle")}
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: "6px", maxHeight: "320px", overflow: "auto" }}>
        {busy && agents.length === 0 ? (
          <div style={agentConfigHintStyle}>{t("agentConfig.loading")}</div>
        ) : agents.length === 0 ? (
          <div style={agentConfigHintStyle}>{t("agentConfig.noAgents")}</div>
        ) : (
          agents.map((item) => {
            const action = item.installed ? "update" : "install";
            const commands = action === "install" ? item.install_commands || [] : item.update_commands || [];
            const disabled = busy || commands.length === 0;
            const actionLabel = item.installed ? t("agentConfig.update") : t("agentConfig.install");
            const description = item.brief || t("agentConfig.noDescription");
            const descriptionExpanded = expandedDescriptions.has(item.name);
            return (
              <div
                key={item.name}
                style={{
                  border: "1px solid var(--border-color)",
                  background: "transparent",
                  color: "var(--text-primary)",
                  borderRadius: "8px",
                  padding: "8px 10px",
                  display: "flex",
                  flexDirection: "column",
                  gap: "4px",
                }}
              >
                <div style={{ display: "flex", alignItems: "center", gap: "8px", minWidth: 0 }}>
                  <AgentIcon
                    agentName={item.name}
                    style={{ width: "15px", height: "15px", display: "block", flexShrink: 0 }}
                  />
                  <div style={{ minWidth: 0, flex: 1, display: "flex", alignItems: "center", gap: "8px" }}>
                    <span style={{ fontSize: "12px", fontWeight: 600, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                      {item.name}
                    </span>
                    <button
                      type="button"
                      disabled={disabled}
                      title={commands.length > 0 ? actionLabel : t("agentConfig.noCommand")}
                      onClick={() => onRun(item, action)}
                      style={{
                        ...agentConfigPrimaryButtonStyle(disabled),
                        marginLeft: "auto",
                        padding: "2px 6px",
                        minWidth: "36px",
                        height: "18px",
                        lineHeight: "12px",
                        fontSize: "11px",
                        borderRadius: "5px",
                        flexShrink: 0,
                      }}
                    >
                      {runningAgent === item.name ? t("agentConfig.starting") : actionLabel}
                    </button>
                  </div>
                </div>
                <div style={{ display: "flex", alignItems: descriptionExpanded ? "flex-start" : "center", gap: "4px", minWidth: 0 }}>
                  <div
                    title={description}
                    style={{
                      fontSize: "11px",
                      color: "var(--text-secondary)",
                      overflow: descriptionExpanded ? "visible" : "hidden",
                      textOverflow: descriptionExpanded ? "clip" : "ellipsis",
                      whiteSpace: descriptionExpanded ? "normal" : "nowrap",
                      width: "100%",
                      lineHeight: "16px",
                      wordBreak: "break-word",
                    }}
                  >
                    {description}
                  </div>
                  <button
                    type="button"
                    aria-label={descriptionExpanded ? t("agentConfig.collapseDescription") : t("agentConfig.expandDescription")}
                    title={descriptionExpanded ? t("common.collapse") : t("common.expand")}
                    onClick={() => {
                      setExpandedDescriptions((current) => {
                        const next = new Set(current);
                        if (next.has(item.name)) {
                          next.delete(item.name);
                        } else {
                          next.add(item.name);
                        }
                        return next;
                      });
                    }}
                    style={{
                      width: "16px",
                      height: "16px",
                      border: "none",
                      background: "transparent",
                      color: "var(--text-secondary)",
                      padding: 0,
                      display: "inline-flex",
                      alignItems: "center",
                      justifyContent: "center",
                      cursor: "pointer",
                      flexShrink: 0,
                      marginTop: descriptionExpanded ? "0" : undefined,
                    }}
                  >
                    <svg
                      width="12"
                      height="12"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="2.4"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      aria-hidden="true"
                      style={{ transform: descriptionExpanded ? "rotate(180deg)" : "rotate(0deg)", transition: "transform 0.15s ease" }}
                    >
                      <path d="m6 9 6 6 6-6" />
                    </svg>
                  </button>
                </div>
              </div>
            );
          })
        )}
      </div>
      {error ? <div style={{ ...agentConfigHintStyle, color: "#dc2626" }}>{error}</div> : null}
    </div>
  );
}

const agentConfigFieldStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "6px",
};

const agentConfigLabelStyle: React.CSSProperties = {
  fontSize: "11px",
  fontWeight: 600,
  color: "var(--text-secondary)",
};

const agentConfigInputStyle: React.CSSProperties = {
  width: "100%",
  borderRadius: "8px",
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-primary)",
  fontSize: "12px",
  padding: "8px 10px",
  outline: "none",
  boxSizing: "border-box",
};

const agentConfigLineEditorStyle: React.CSSProperties = {
  ...agentConfigInputStyle,
  minHeight: "42px",
  maxHeight: "260px",
  overflow: "auto",
  padding: "6px",
  display: "flex",
  flexDirection: "column",
  gap: "4px",
};

const agentConfigLineTextAreaStyle: React.CSSProperties = {
  width: "100%",
  border: "none",
  borderRadius: "6px",
  background: "rgba(148, 163, 184, 0.16)",
  color: "var(--text-primary)",
  fontSize: "12px",
  padding: "4px 8px",
  outline: "none",
  boxSizing: "border-box",
  minHeight: "28px",
  resize: "none",
  lineHeight: "20px",
  overflow: "hidden",
  fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
};

const agentConfigHintStyle: React.CSSProperties = {
  borderRadius: "8px",
  padding: "8px 10px",
  fontSize: "12px",
  color: "var(--text-secondary)",
  background: "rgba(148, 163, 184, 0.10)",
  lineHeight: 1.45,
  wordBreak: "break-word",
};

export const agentConfigActionRowStyle: React.CSSProperties = {
  display: "grid",
  gridTemplateColumns: "1fr 1fr",
  gap: "8px",
};

export const agentConfigPrimaryButtonStyle = (disabled: boolean): React.CSSProperties => ({
  border: "none",
  background: "var(--accent-color)",
  color: "#fff",
  borderRadius: "8px",
  padding: "8px 10px",
  fontSize: "12px",
  fontWeight: 600,
  cursor: disabled ? "not-allowed" : "pointer",
  opacity: disabled ? 0.6 : 1,
});

export const agentConfigSecondaryButtonStyle = (disabled: boolean): React.CSSProperties => ({
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-secondary)",
  borderRadius: "8px",
  padding: "8px 10px",
  fontSize: "12px",
  fontWeight: 600,
  cursor: disabled ? "not-allowed" : "pointer",
  opacity: disabled ? 0.6 : 1,
});

const agentConfigIconButtonStyle = (disabled: boolean): React.CSSProperties => ({
  width: "28px",
  height: "28px",
  border: "none",
  borderRadius: "8px",
  background: "transparent",
  color: "#dc2626",
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  cursor: disabled ? "not-allowed" : "pointer",
  opacity: disabled ? 0.5 : 0.9,
  flexShrink: 0,
});
