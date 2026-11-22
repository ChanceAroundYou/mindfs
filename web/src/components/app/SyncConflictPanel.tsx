// 控制面同步的冲突面板：按域分组、逐条裁决、批量裁决（一次确认一次提交）。
//
// 和横幅同样的分工：这里只渲染与点击；打开、读取、裁决、刷新都在
// useSyncConflicts。视图不能直接 import 网络层（仓库架构红线）。
//
// 挂到 document.body：主区有 contain: paint，fixed 浮层留在 App 树里会被顶偏。
import React, { useMemo, useState } from "react";
import { createPortal } from "react-dom";
import { PanelShell } from "../shell/PanelShell";
import { useI18n } from "../../i18n";
import { useSyncConflicts } from "../../hooks/useSyncConflicts";
import {
  conflictChoiceKeys,
  conflictSideSummary,
  groupSyncConflicts,
  type SyncConflictChoice,
  type SyncConflictView,
} from "../../shared/syncConflicts";

const CARD_STYLE: React.CSSProperties = {
  border: "1px solid var(--border-color)",
  borderRadius: 10,
  padding: "10px 12px",
  display: "flex",
  flexDirection: "column",
  gap: 8,
  background: "var(--panel-bg)",
};

const SIDE_STYLE: React.CSSProperties = {
  border: "1px solid var(--border-color)",
  borderRadius: 8,
  padding: "6px 8px",
  minWidth: 0,
};

const PRE_STYLE: React.CSSProperties = {
  margin: 0,
  maxHeight: 120,
  overflow: "auto",
  fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
  fontSize: 11,
  lineHeight: 1.4,
  whiteSpace: "pre-wrap",
  overflowWrap: "anywhere",
  color: "var(--text-secondary)",
};

const BUTTON_STYLE: React.CSSProperties = {
  padding: "3px 10px",
  borderRadius: 7,
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-primary, inherit)",
  font: "inherit",
  fontSize: 12,
  cursor: "pointer",
};

function SideBox({ label, text }: { label: string; text: string }) {
  return (
    <div style={SIDE_STYLE}>
      <div style={{ fontSize: 11, color: "var(--text-secondary)", marginBottom: 4 }}>{label}</div>
      <pre style={PRE_STYLE}>{text}</pre>
    </div>
  );
}

export function SyncConflictPanel() {
  const { t } = useI18n();
  const { open, close, conflicts, busy, error, reload, resolveOne, resolveMany } =
    useSyncConflicts();
  // 批量裁决先进入待确认态；确认条里写明条数，确认后只提交一次。
  const [pendingBatch, setPendingBatch] = useState<{
    domain: string;
    choice: Exclude<SyncConflictChoice, "payload">;
  } | null>(null);
  const [manual, setManual] = useState<{ id: string; text: string } | null>(null);
  const [manualInvalid, setManualInvalid] = useState(false);

  const groups = useMemo(() => groupSyncConflicts(conflicts ?? []), [conflicts]);

  if (!open) return null;

  const sideText = (conflict: SyncConflictView, side: "local" | "remote"): string => {
    const summary = conflictSideSummary(conflict, side);
    if (summary.kind === "deleted") return t("syncConflict.deleted");
    if (summary.kind === "redacted") return t("syncConflict.passwordChanged");
    if (summary.kind === "missing") return "—";
    return summary.text;
  };

  const submitManual = async () => {
    if (!manual) return;
    let parsed: unknown;
    try {
      parsed = JSON.parse(manual.text);
    } catch {
      setManualInvalid(true);
      return;
    }
    setManualInvalid(false);
    const ok = await resolveOne(manual.id, "payload", parsed);
    if (ok) setManual(null);
  };

  return createPortal(
    <PanelShell
      title={t("syncConflict.title")}
      width={880}
      onClose={close}
      showCloseButton
    >
      <div style={{ padding: "12px 16px", display: "flex", flexDirection: "column", gap: 12 }}>
        {error ? (
          <div
            role="alert"
            style={{
              display: "flex",
              alignItems: "center",
              gap: 8,
              padding: "6px 10px",
              borderRadius: 8,
              border: "1px solid color-mix(in srgb, var(--status-bad) 45%, transparent)",
              background: "color-mix(in srgb, var(--status-bad) 10%, transparent)",
              fontSize: 12,
            }}
          >
            <span style={{ flex: 1, minWidth: 0 }}>
              {error === "load" ? t("syncConflict.loadFailed") : t("syncConflict.resolveFailed")}
            </span>
            <button type="button" style={BUTTON_STYLE} onClick={() => void reload()}>
              {t("syncConflict.retry")}
            </button>
          </div>
        ) : null}

        {busy ? (
          <div style={{ fontSize: 12, color: "var(--text-secondary)" }}>
            {t("syncConflict.resolving")}
          </div>
        ) : null}

        {conflicts !== null && conflicts.length === 0 ? (
          <div style={{ fontSize: 13, color: "var(--text-secondary)" }}>
            {t("syncConflict.empty")}
          </div>
        ) : null}

        {groups.map((group) => {
          const batch = pendingBatch?.domain === group.domain ? pendingBatch : null;
          return (
            <section key={group.domain} style={{ display: "flex", flexDirection: "column", gap: 8 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <strong style={{ fontSize: 13 }}>
                  {group.label ? t(group.label) : group.domain}
                </strong>
                <span style={{ fontSize: 12, color: "var(--text-secondary)" }}>
                  {t("syncConflict.count", { count: group.conflicts.length })}
                </span>
                <span style={{ flex: 1 }} />
                <button
                  type="button"
                  style={BUTTON_STYLE}
                  disabled={busy}
                  onClick={() => setPendingBatch({ domain: group.domain, choice: "local" })}
                >
                  {t("syncConflict.batchKeepLocal")}
                </button>
                <button
                  type="button"
                  style={BUTTON_STYLE}
                  disabled={busy}
                  onClick={() => setPendingBatch({ domain: group.domain, choice: "primary" })}
                >
                  {t("syncConflict.batchUsePrimary")}
                </button>
              </div>

              {batch ? (
                <div
                  role="alertdialog"
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: 8,
                    padding: "6px 10px",
                    borderRadius: 8,
                    border: "1px solid color-mix(in srgb, var(--status-warn) 45%, transparent)",
                    background: "color-mix(in srgb, var(--status-warn) 10%, transparent)",
                    fontSize: 12,
                  }}
                >
                  <span style={{ flex: 1, minWidth: 0 }}>
                    {t("syncConflict.batchConfirm", { count: group.conflicts.length })}
                  </span>
                  <button
                    type="button"
                    style={BUTTON_STYLE}
                    disabled={busy}
                    onClick={() => {
                      setPendingBatch(null);
                      void resolveMany(group.conflicts, batch.choice);
                    }}
                  >
                    {t("common.confirm")}
                  </button>
                  <button type="button" style={BUTTON_STYLE} onClick={() => setPendingBatch(null)}>
                    {t("common.cancel")}
                  </button>
                </div>
              ) : null}

              {group.conflicts.map((conflict) => {
                const choices = conflictChoiceKeys(conflict);
                const editing = manual?.id === conflict.id;
                return (
                  <div key={conflict.id} style={CARD_STYLE}>
                    <div
                      style={{
                        fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
                        fontSize: 12,
                        overflowWrap: "anywhere",
                      }}
                    >
                      {conflict.key}
                    </div>
                    <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}>
                      <SideBox label={t("syncConflict.localSide")} text={sideText(conflict, "local")} />
                      <SideBox
                        label={t("syncConflict.remoteSide")}
                        text={sideText(conflict, "remote")}
                      />
                    </div>

                    {editing && manual ? (
                      <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                        <textarea
                          value={manual.text}
                          rows={8}
                          onChange={(event) => setManual({ id: conflict.id, text: event.target.value })}
                          style={{
                            width: "100%",
                            boxSizing: "border-box",
                            fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
                            fontSize: 12,
                            borderRadius: 8,
                            border: "1px solid var(--border-color)",
                            background: "var(--input-bg, transparent)",
                            color: "var(--text-primary, inherit)",
                            padding: 8,
                          }}
                        />
                        {manualInvalid ? (
                          <span style={{ fontSize: 12, color: "var(--status-bad)" }}>
                            {t("syncConflict.payloadInvalid")}
                          </span>
                        ) : null}
                        <div style={{ display: "flex", gap: 8 }}>
                          <button
                            type="button"
                            style={BUTTON_STYLE}
                            disabled={busy}
                            onClick={() => void submitManual()}
                          >
                            {t("syncConflict.submitMerge")}
                          </button>
                          <button
                            type="button"
                            style={BUTTON_STYLE}
                            onClick={() => {
                              setManual(null);
                              setManualInvalid(false);
                            }}
                          >
                            {t("common.cancel")}
                          </button>
                        </div>
                      </div>
                    ) : (
                      <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                        <button
                          type="button"
                          style={BUTTON_STYLE}
                          disabled={busy}
                          onClick={() => void resolveOne(conflict.id, "local")}
                        >
                          {t("syncConflict.keepLocal")}
                        </button>
                        <button
                          type="button"
                          style={BUTTON_STYLE}
                          disabled={busy}
                          onClick={() => void resolveOne(conflict.id, "primary")}
                        >
                          {t("syncConflict.usePrimary")}
                        </button>
                        {choices.includes("payload") ? (
                          <button
                            type="button"
                            style={BUTTON_STYLE}
                            disabled={busy || conflict.remote_deleted === true}
                            onClick={() => {
                              setManualInvalid(false);
                              setManual({ id: conflict.id, text: sideText(conflict, "remote") });
                            }}
                          >
                            {t("syncConflict.mergeManually")}
                          </button>
                        ) : null}
                      </div>
                    )}
                  </div>
                );
              })}
            </section>
          );
        })}
      </div>
    </PanelShell>,
    document.body,
  );
}