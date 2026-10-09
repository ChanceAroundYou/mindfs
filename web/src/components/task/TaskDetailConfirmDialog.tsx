import React from "react";
import { useI18n } from "../../i18n";
import { panelButtonStyle } from "../shell/PanelShell";

type PendingConfirm = { type: "discard"; index: number } | { type: "remove"; index: number } | null;

export function TaskDetailConfirmDialog({
  pendingConfirm,
  saving,
  onCancel,
  onConfirm,
}: {
  pendingConfirm: PendingConfirm;
  saving: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const { t } = useI18n();
  if (!pendingConfirm) return null;

  return (
    <div
      style={{ position: "fixed", inset: 0, zIndex: 96, background: "rgba(15, 23, 42, 0.28)", display: "flex", alignItems: "center", justifyContent: "center", padding: "24px" }}
      onMouseDown={(event) => { if (event.target === event.currentTarget) onCancel(); }}
    >
      <section style={{ width: "min(420px, 100%)", borderRadius: "10px", border: "1px solid var(--border-color)", background: "var(--menu-bg)", boxShadow: "0 24px 60px rgba(15, 23, 42, 0.24)", overflow: "hidden" }}>
        <div style={{ padding: "12px 14px", borderBottom: "1px solid var(--border-color)", display: "flex", alignItems: "center", gap: "10px" }}>
          <div style={{ minWidth: 0, fontSize: "13px", fontWeight: 800, color: "var(--text-color)" }}>
            {pendingConfirm.type === "discard" ? t("task.discardDraftTitle") : t("task.removeStageTitle")}
          </div>
        </div>
        <div style={{ padding: "14px", display: "flex", flexDirection: "column", gap: "12px" }}>
          <div style={{ fontSize: "12px", lineHeight: 1.5, color: "var(--text-secondary)" }}>
            {pendingConfirm.type === "discard" ? t("task.discardDraftMessage") : t("task.removeStageMessage")}
          </div>
          <div style={{ display: "flex", justifyContent: "flex-end", gap: "8px" }}>
            <button type="button" onClick={onCancel} style={panelButtonStyle("secondary")}>
              {pendingConfirm.type === "discard" ? t("task.keepEditing") : t("common.cancel")}
            </button>
            <button
              type="button"
              disabled={saving}
              onClick={onConfirm}
              style={panelButtonStyle("danger")}
            >
              {pendingConfirm.type === "discard" ? t("task.discardDraft") : t("common.delete")}
            </button>
          </div>
        </div>
      </section>
    </div>
  );
}
