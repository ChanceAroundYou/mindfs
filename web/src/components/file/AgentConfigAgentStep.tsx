import { useI18n } from "../../i18n/index";
import type { AgentStatus } from "../../shared/agentTypes";
import { AgentMenuList } from "../agent/AgentMenuList";
import { RestartSpinner } from "./icons";
import { agentConfigHintStyle, agentConfigSecondaryButtonStyle } from "./agentConfigStyles";

export type AgentConfigAgentStepProps = {
  title: string;
  flow: "backup" | "switch";
  agents: AgentStatus[];
  busy: boolean;
  selectedAgent: string;
  restartingAgent: string;
  onRestartAgent?: (agentName: string) => void | Promise<void>;
  onChooseAgent: (name: string) => void;
};

export function AgentConfigAgentStep({
  title,
  flow,
  agents,
  busy,
  selectedAgent,
  restartingAgent,
  onRestartAgent,
  onChooseAgent,
}: AgentConfigAgentStepProps) {
  const { t } = useI18n();
  return (
    <>
      <div style={{ fontSize: "12px", fontWeight: 700, color: "var(--text-primary)" }}>
        {title}
      </div>
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
              if (!name) return null;
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
  );
}
