import { useState } from "react";
import { AgentIcon } from "../agent/AgentIcon";
import { useI18n } from "../../i18n/index";
import type { AgentStatus } from "../../shared/agentTypes";
import { agentConfigHintStyle, agentConfigPrimaryButtonStyle } from "./agentConfigStyles";

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
  const [expandedDescriptions, setExpandedDescriptions] = useState<Set<string>>(() => new Set());

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
