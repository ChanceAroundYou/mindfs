// 侧栏 ⋯「会话体检」：跨项目列出需要处理的会话与运行时问题，一键修复 + 清死锁。
// 这里只渲染与点击；读取、巡检、清理全在 useSessionMaintenance，视图不 import services。
import React from "react";
import { createPortal } from "react-dom";
import { useSessionMaintenance } from "../../hooks/useSessionMaintenance";
import { useI18n } from "../../i18n/index";
import {
  SESSION_MAINTENANCE_EVENT,
  formatMaintenanceBytes,
  groupMaintenanceEntries,
  groupRuntimeIssues,
  shortSessionKey,
} from "../../shared/sessionMaintenance";
import { PanelShell } from "../shell/PanelShell";

const BUTTON_STYLE: React.CSSProperties = {
  padding: "4px 10px",
  borderRadius: 7,
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-primary, inherit)",
  font: "inherit",
  fontSize: 12,
  cursor: "pointer",
};

const ROW_STYLE: React.CSSProperties = {
  padding: "8px 10px",
  borderRadius: 8,
  border: "1px solid var(--border-color)",
  marginBottom: 6,
  fontSize: 12,
};

export type SessionHealthPanelProps = {
  rootIds?: string[];
};

export function SessionHealthPanel({ rootIds = [] }: SessionHealthPanelProps) {
  const control = useSessionMaintenance({ mode: "health", rootIds });
  const { t } = useI18n();
  if (!control.open) return null;

  const groups = groupRuntimeIssues(control.issues);
  const diagnoseGroups = groupMaintenanceEntries(control.report);
  const busy = control.busy !== null;

  const openSession = (sessionKey: string, rootId?: string, nodeId?: string, agent?: string) => {
    window.dispatchEvent(
      new CustomEvent(SESSION_MAINTENANCE_EVENT, {
        detail: { sessionKey, rootId, nodeId, agent },
      }),
    );
  };

  return createPortal(
    <PanelShell title={t("sessionHealth.title")} width={640} showCloseButton onClose={control.close}>
      <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
        <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
          <button type="button" style={BUTTON_STYLE} disabled={busy} onClick={() => void control.reload()}>
            {t("sessionHealth.reload")}
          </button>
          <button type="button" style={BUTTON_STYLE} disabled={busy} onClick={() => void control.sweepRuntime()}>
            {t("sessionHealth.sweep")}
          </button>
          <button
            type="button"
            style={BUTTON_STYLE}
            disabled={busy || !control.report?.stale_locks?.length}
            onClick={() => void control.cleanLocks()}
          >
            {t("sessionHealth.cleanLocks")}
          </button>
        </div>

        {control.report ? (
          <div style={{ fontSize: 12, color: "var(--text-secondary)" }}>
            {t("sessionHealth.scannedRoots", { count: control.report.scanned })}
          </div>
        ) : null}

        {control.issues.length === 0 && control.report ? (
          <div style={{ fontSize: 13, color: "var(--text-secondary)" }}>{t("sessionHealth.noIssues")}</div>
        ) : null}

        {groups.map((group) => (
          <section key={group.className}>
            <h3 style={{ fontSize: 13, margin: "0 0 6px" }}>
              {t(group.label)} · {t("sessionHealth.issues", { count: group.issues.length })}
            </h3>
            {group.issues.map((issue) => (
              <div key={`${issue.root_id ?? ""}:${issue.session_key}`} style={ROW_STYLE}>
                <div>{shortSessionKey(issue.session_key)}</div>
                <div style={{ color: "var(--text-secondary)", overflowWrap: "anywhere" }}>
                  {issue.agent} · {issue.path}
                  {issue.root_id ? ` · ${issue.root_id}` : ""}
                </div>
                <button
                  type="button"
                  style={{ ...BUTTON_STYLE, marginTop: 6 }}
                  onClick={() => openSession(issue.session_key, issue.root_id, undefined, issue.agent)}
                >
                  {t("sessionHealth.openDetail")}
                </button>
              </div>
            ))}
          </section>
        ))}

        {diagnoseGroups.map((group) => (
          <section key={group.key}>
            <h3 style={{ fontSize: 13, margin: "0 0 6px" }}>{t(group.label)}</h3>
            {group.entries.map((entry) => (
              <div key={entry.session_key} style={ROW_STYLE}>
                <div>{shortSessionKey(entry.session_key)}</div>
                <div style={{ color: "var(--text-secondary)" }}>
                  {entry.agent} · {formatMaintenanceBytes(entry.bytes)} · {formatMaintenanceBytes(entry.image_bytes)}
                </div>
                <button
                  type="button"
                  style={{ ...BUTTON_STYLE, marginTop: 6 }}
                  onClick={() => openSession(entry.session_key, undefined, undefined, entry.agent)}
                >
                  {t("sessionHealth.openDetail")}
                </button>
              </div>
            ))}
          </section>
        ))}

        {control.sweep ? (
          <div style={{ fontSize: 12 }}>
            {t("sessionHealth.sweepResult", {
              repaired: control.sweep.repaired,
              skipped: control.sweep.skipped,
              failed: control.sweep.failed,
            })}
            {control.sweep.details?.length ? (
              <pre style={{ whiteSpace: "pre-wrap", fontSize: 11, color: "var(--text-secondary)" }}>
                {control.sweep.details.join("\n")}
              </pre>
            ) : null}
          </div>
        ) : null}

        {control.lockResult ? (
          <div style={{ fontSize: 12 }}>
            {t("sessionMaintenance.lockResult", {
              removed: control.lockResult.removed?.length ?? 0,
              failed: control.lockResult.failed?.length ?? 0,
            })}
          </div>
        ) : null}

        {busy ? <div style={{ fontSize: 12, color: "var(--text-secondary)" }}>{t("sessionMaintenance.busy")}</div> : null}
        {control.error ? (
          <div role="alert" style={{ fontSize: 12, color: "var(--status-bad)" }}>
            {t("sessionMaintenance.failed", { reason: control.error })}
          </div>
        ) : null}
      </div>
    </PanelShell>,
    document.body,
  );
}
