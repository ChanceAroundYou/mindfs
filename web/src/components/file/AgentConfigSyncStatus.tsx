import React from "react";
import { useI18n } from "../../i18n/index";
import type { AgentConfigSyncResult } from "../../shared/agentTypes";
import { agentConfigActionRowStyle, agentConfigHintStyle, agentConfigSecondaryButtonStyle } from "./agentConfigStyles";

type AgentConfigSyncStatusProps = {
  status: AgentConfigSyncResult | null;
  busy: boolean;
  onRetry: () => void;
};

export function AgentConfigSyncStatus({ status, busy, onRetry }: AgentConfigSyncStatusProps) {
  const { t } = useI18n();
  const state = status?.state || "idle";
  const label = t(`agentConfig.syncState.${state}`);
  return (
    <div style={{ ...agentConfigHintStyle, border: "1px solid var(--border-color)", borderRadius: "8px", padding: "8px" }}>
      <div style={agentConfigActionRowStyle}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontWeight: 700, color: state === "conflict" || state === "invalid" ? "#dc2626" : "var(--text-primary)" }}>
            {t("agentConfig.syncStatus", { state: label })}
          </div>
          {status?.message ? <div style={{ marginTop: "3px", overflowWrap: "anywhere" }}>{status.message}</div> : null}
        </div>
        <button
          type="button"
          disabled={busy || state === "syncing"}
          onClick={onRetry}
          style={agentConfigSecondaryButtonStyle(busy || state === "syncing")}
        >
          {busy || state === "syncing" ? t("agentConfig.syncing") : t("agentConfig.retrySync")}
        </button>
      </div>
      {status?.last_success_at ? (
        <div style={{ marginTop: "4px" }}>{t("agentConfig.lastSync", { time: status.last_success_at })}</div>
      ) : null}
    </div>
  );
}
