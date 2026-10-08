import React from "react";

export function FileTreeSessionNamingPopover(props: any) {
  const { sessionNamingAgent, sessionNamingModel, sessionNamingAgents, sessionNamingBusy, sessionNamingDisabled, sessionNamingError, sessionNamingOpen, setSessionNamingAgent, setSessionNamingModel, setSessionNamingDisabled, setSessionNamingOpen, saveSessionNaming, t, AgentSelector, agentConfigActionRowStyle, agentConfigSecondaryButtonStyle, agentConfigPrimaryButtonStyle } = props;
  return (sessionNamingOpen ? (
          <div
            style={{
              position: "absolute",
              top: "calc(100% + 6px)",
              left: "8px",
              right: "3px",
              zIndex: 40,
              padding: "14px",
              borderRadius: "12px",
              border: "1px solid var(--border-color)",
              background: "var(--menu-bg)",
              boxShadow: "0 16px 36px rgba(15, 23, 42, 0.18)",
            }}
          >
            <div style={{ fontSize: "13px", fontWeight: 700, color: "var(--text-primary)" }}>
              {t("sessionNaming.title")}
            </div>
            <div
              aria-disabled={sessionNamingDisabled || sessionNamingBusy}
              style={{
                marginTop: "12px",
                minHeight: "42px",
                padding: "6px 8px",
                border: "1px solid var(--border-color)",
                borderRadius: "10px",
                display: "flex",
                alignItems: "center",
                justifyContent: "flex-start",
                gap: "8px",
                opacity: sessionNamingDisabled ? 0.5 : 1,
                pointerEvents: sessionNamingDisabled || sessionNamingBusy ? "none" : "auto",
                transition: "opacity 0.15s ease",
              }}
            >
              {sessionNamingAgent ? (
                <AgentSelector
                  agent={sessionNamingAgent}
                  model={sessionNamingModel}
                  agents={sessionNamingAgents}
                  onAgentChange={(agent: string, model?: string) => {
                    setSessionNamingAgent(agent);
                    setSessionNamingModel(model || "");
                  }}
                  compact
                  showChevron
                  menuPlacement="bottom"
                  defaultExpandOptions
                  viewportMenu
                  allowDefaultModel
                />
              ) : null}
              <span style={{ minWidth: 0, marginLeft: "auto", fontSize: "12px", color: "var(--text-secondary)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                {sessionNamingBusy ? t("common.loading") : sessionNamingModel || t("agent.defaultModel")}
              </span>
            </div>
            <label
              style={{
                marginTop: "12px",
                display: "flex",
                alignItems: "center",
                gap: "8px",
                color: "var(--text-primary)",
                fontSize: "13px",
                cursor: sessionNamingBusy ? "default" : "pointer",
              }}
            >
              <input
                type="checkbox"
                checked={sessionNamingDisabled}
                disabled={sessionNamingBusy}
                onChange={(event) => setSessionNamingDisabled(event.target.checked)}
              />
              <span>{t("sessionNaming.disableAutoRename")}</span>
            </label>
            {sessionNamingError ? (
              <div style={{ marginTop: "8px", color: "#dc2626", fontSize: "11px", lineHeight: 1.4 }}>
                {sessionNamingError}
              </div>
            ) : null}
            <div style={{ ...agentConfigActionRowStyle, marginTop: "12px" }}>
              <button
                type="button"
                disabled={sessionNamingBusy}
                onClick={() => setSessionNamingOpen(false)}
                style={agentConfigSecondaryButtonStyle(sessionNamingBusy)}
              >
                {t("common.cancel")}
              </button>
              <button
                type="button"
                disabled={sessionNamingBusy || (!sessionNamingAgent && !sessionNamingDisabled)}
                onClick={() => void saveSessionNaming()}
                style={agentConfigPrimaryButtonStyle(sessionNamingBusy || (!sessionNamingAgent && !sessionNamingDisabled))}
              >
                {sessionNamingBusy ? t("common.saving") : t("common.save")}
              </button>
            </div>
          </div>
        ) : null );
}
