import React from "react";
import { useI18n } from "../../i18n/index";

// 供应商页签顶部的操作行：一键同步模型 + 展开新增表单。
export function AgentConfigProviderActions({ busy, syncBusy, hasProviders, showForm, onSyncAll, onToggleForm }: {
  busy: boolean;
  syncBusy: boolean;
  hasProviders: boolean;
  showForm: boolean;
  onSyncAll: () => void;
  onToggleForm: (show: boolean) => void;
}) {
  const { t } = useI18n();
  const syncDisabled = busy || syncBusy || !hasProviders;
  return (
    <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
      <button
        type="button"
        disabled={syncDisabled}
        onClick={onSyncAll}
        style={{
          border: "1px solid var(--border-color)",
          background: "transparent",
          color: syncDisabled ? "var(--text-secondary)" : "var(--accent-color)",
          borderRadius: "8px",
          padding: "5px 10px",
          fontSize: "11px",
          fontWeight: 600,
          cursor: syncDisabled ? "default" : "pointer",
        }}
      >
        {syncBusy ? t("agentConfig.syncingProviders") : t("agentConfig.syncAllProviders")}
      </button>
      <button
        type="button"
        disabled={busy}
        onClick={() => onToggleForm(!showForm)}
        style={{
          border: "1px solid var(--border-color)",
          background: showForm ? "var(--selection-bg)" : "transparent",
          color: showForm ? "var(--accent-color)" : "var(--text-primary)",
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
  );
}