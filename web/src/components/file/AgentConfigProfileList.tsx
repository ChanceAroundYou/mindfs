import React from "react";
import { useI18n } from "../../i18n/index";
import type { AgentConfigBackup } from "../../shared/agentTypes";
import { TrashIcon } from "./icons";
import { agentConfigHintStyle, agentConfigIconButtonStyle } from "./agentConfigStyles";

type AgentConfigProfileListProps = {
  backups: AgentConfigBackup[];
  selectedID: string;
  busy: boolean;
  onSelect: (id: string) => void;
  onDelete: (id: string) => void;
};

export function AgentConfigProfileList({ backups, selectedID, busy, onSelect, onDelete }: AgentConfigProfileListProps) {
  const { t } = useI18n();
  const shared = backups.filter((item) => item.source === "shared");
  const local = backups.filter((item) => item.source !== "shared");

  const renderEntry = (item: AgentConfigBackup, isShared: boolean) => {
    const selected = item.id === selectedID;
    return (
      <div
        key={item.id}
        onClick={() => onSelect(item.id)}
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
          {!isShared ? (
            <button
              type="button"
              aria-label={t("agentConfig.deleteConfig", { name: item.name })}
              title={t("common.delete")}
              disabled={busy}
              onClick={(event) => {
                event.stopPropagation();
                onDelete(item.id);
              }}
              style={agentConfigIconButtonStyle(busy)}
            >
              <TrashIcon />
            </button>
          ) : null}
        </div>
        <div style={{ marginTop: "3px", fontSize: "10px", color: "var(--text-secondary)" }}>
          {t(isShared ? "agentConfig.sharedProfile" : "agentConfig.localSnapshot")}
        </div>
        {item.localCredentialAvailable === false ? (
          <div style={{ marginTop: "3px", fontSize: "10px", color: "var(--text-secondary)" }}>{t("agentConfig.llmuxMissingKey")}</div>
        ) : null}
      </div>
    );
  };

  if (backups.length === 0) {
    return <div style={agentConfigHintStyle}>{t("agentConfig.noBackups")}</div>;
  }
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "10px", maxHeight: "260px", overflow: "auto" }}>
      {shared.length > 0 ? (
        <section style={{ display: "flex", flexDirection: "column", gap: "6px" }}>
          <div style={{ fontSize: "11px", fontWeight: 700, color: "var(--text-secondary)" }}>{t("agentConfig.sharedProfiles")}</div>
          {shared.map((item) => renderEntry(item, true))}
        </section>
      ) : null}
      {local.length > 0 ? (
        <section style={{ display: "flex", flexDirection: "column", gap: "6px" }}>
          <div style={{ fontSize: "11px", fontWeight: 700, color: "var(--text-secondary)" }}>{t("agentConfig.localSnapshots")}</div>
          {local.map((item) => renderEntry(item, false))}
        </section>
      ) : null}
    </div>
  );
}
