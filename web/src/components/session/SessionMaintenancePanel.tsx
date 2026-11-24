// 会话标题栏 ⋯「会话维护」对应的面板。
//
// 只做无损维护：诊断、清图片（备份后移除）、清陈旧锁、删空会话。不做思考剥离、
// 工具负载截断、--drop-tools 或任何对话正文改写。这里只渲染与点击，请求与状态在
// useSessionMaintenance；fixed 浮层必须 portal 到 body，否则被主区 contain: paint 顶偏。
import React, { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { useSessionMaintenance } from "../../hooks/useSessionMaintenance";
import { useI18n } from "../../i18n/index";
import {
  SESSION_MAINTENANCE_EVENT,
  formatMaintenanceBytes,
  groupMaintenanceEntries,
  maintenanceCapabilities,
  maintenanceCapabilityKeys,
  maintenanceReclaim,
  shortSessionKey,
  type MaintenanceEntry,
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
};

function CapabilityLine({ entry, canRepoint }: { entry: MaintenanceEntry; canRepoint: boolean }) {
  const { t } = useI18n();
  return (
    <div style={{ fontSize: 11, color: "var(--text-secondary)" }}>
      {maintenanceCapabilityKeys(entry, canRepoint).map((key) => t(key)).join(" · ")}
    </div>
  );
}

export function SessionMaintenancePanel() {
  const control = useSessionMaintenance({ mode: "session" });
  const { t } = useI18n();
  const [confirmImages, setConfirmImages] = useState(false);
  const [confirmPurge, setConfirmPurge] = useState(false);

  const sessionKey = control.target?.sessionKey;
  useEffect(() => {
    setConfirmImages(false);
    setConfirmPurge(false);
  }, [sessionKey]);

  if (!control.open) return null;

  const entry = control.report?.sessions.find((item) => item.session_key === sessionKey);
  const groups = groupMaintenanceEntries(control.report);
  const capabilities = entry ? maintenanceCapabilities(entry) : null;
  const reclaim = entry ? maintenanceReclaim(entry.bytes, control.lastClean) : null;
  const busy = control.busy !== null;

  const openEntry = (key: string) => {
    window.dispatchEvent(
      new CustomEvent(SESSION_MAINTENANCE_EVENT, {
        detail: { ...control.target, sessionKey: key },
      }),
    );
  };

  return createPortal(
    <PanelShell
      title={t("sessionMaintenance.title")}
      width={640}
      showCloseButton
      onClose={control.close}
    >
      <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
        <div style={{ fontSize: 12, color: "var(--text-secondary)" }}>{t("sessionMaintenance.hint")}</div>

        <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
          <span style={{ fontSize: 13, fontWeight: 600 }}>{shortSessionKey(sessionKey ?? "")}</span>
          {entry ? <CapabilityLine entry={entry} canRepoint={control.canRepoint} /> : null}
          <button type="button" style={BUTTON_STYLE} disabled={busy} onClick={() => void control.reload()}>
            {t("sessionMaintenance.reload")}
          </button>
        </div>

        {entry && reclaim ? (
          <div style={ROW_STYLE}>
            <div style={{ fontSize: 12 }}>
              {t("sessionMaintenance.before", { size: formatMaintenanceBytes(reclaim.before) })}
              {" → "}
              {t("sessionMaintenance.after", { size: formatMaintenanceBytes(reclaim.after) })}
            </div>
            {reclaim.reclaimed > 0 ? (
              <div style={{ fontSize: 12, color: "var(--text-secondary)" }}>
                {t("sessionMaintenance.reclaimed", {
                  size: formatMaintenanceBytes(reclaim.reclaimed),
                  count: reclaim.removedImages,
                })}
              </div>
            ) : null}
            {entry.error ? (
              <div style={{ fontSize: 12 }}>{t("sessionMaintenance.error", { reason: entry.error })}</div>
            ) : null}
            {entry.unsupported ? (
              <div style={{ fontSize: 12 }}>
                {t("sessionMaintenance.unsupported", { reason: entry.unsupported })}
              </div>
            ) : null}
            <div style={{ display: "flex", gap: 6, flexWrap: "wrap", marginTop: 8 }}>
              {capabilities?.cleanImages ? (
                !confirmImages ? (
                  <button type="button" style={BUTTON_STYLE} disabled={busy} onClick={() => setConfirmImages(true)}>
                    {t("sessionMaintenance.cleanImages")}
                  </button>
                ) : (
                  <>
                    <button
                      type="button"
                      style={BUTTON_STYLE}
                      disabled={busy}
                      onClick={() => {
                        setConfirmImages(false);
                        void control.cleanImages(sessionKey ?? "", control.target?.rootId);
                      }}
                    >
                      {t("sessionMaintenance.confirmCleanImages")}
                    </button>
                    <button type="button" style={BUTTON_STYLE} onClick={() => setConfirmImages(false)}>
                      {t("sessionMaintenance.cancel")}
                    </button>
                  </>
                )
              ) : null}
              {capabilities?.purgeEmpty ? (
                !confirmPurge ? (
                  <button type="button" style={BUTTON_STYLE} disabled={busy} onClick={() => setConfirmPurge(true)}>
                    {t("sessionMaintenance.purgeEmpty")}
                  </button>
                ) : (
                  <>
                    <button
                      type="button"
                      style={BUTTON_STYLE}
                      disabled={busy}
                      onClick={() => {
                        setConfirmPurge(false);
                        void control.purgeEmpty(sessionKey ?? "", control.target?.rootId);
                      }}
                    >
                      {t("sessionMaintenance.confirmPurge")}
                    </button>
                    <button type="button" style={BUTTON_STYLE} onClick={() => setConfirmPurge(false)}>
                      {t("sessionMaintenance.cancel")}
                    </button>
                  </>
                )
              ) : null}
            </div>
          </div>
        ) : null}

        {control.report ? (
          <div style={{ fontSize: 12, color: "var(--text-secondary)" }}>
            {t("sessionMaintenance.scanned", { count: control.report.scanned })}
          </div>
        ) : null}

        {groups.length === 0 && control.report ? (
          <div style={{ fontSize: 13, color: "var(--text-secondary)" }}>{t("sessionMaintenance.empty")}</div>
        ) : null}

        {groups.map((group) => (
          <section key={group.key}>
            <h3 style={{ fontSize: 13, margin: "0 0 6px" }}>{t(group.label)}</h3>
            {group.entries.map((item) => (
              <button
                key={item.session_key}
                type="button"
                style={{ ...ROW_STYLE, width: "100%", textAlign: "left", cursor: "pointer" }}
                onClick={() => openEntry(item.session_key)}
              >
                <div style={{ fontSize: 12 }}>{shortSessionKey(item.session_key)}</div>
                <div style={{ fontSize: 11, color: "var(--text-secondary)" }}>
                  {item.agent} · {formatMaintenanceBytes(item.bytes)} · {formatMaintenanceBytes(item.image_bytes)}
                </div>
              </button>
            ))}
          </section>
        ))}

        {control.report?.stale_locks?.length ? (
          <section>
            <h3 style={{ fontSize: 13, margin: "0 0 6px" }}>
              {t("sessionMaintenance.locks", { count: control.report.stale_locks.length })}
            </h3>
            <button type="button" style={BUTTON_STYLE} disabled={busy} onClick={() => void control.cleanLocks()}>
              {t("sessionMaintenance.cleanLocks")}
            </button>
            {control.lockResult ? (
              <div style={{ fontSize: 12, marginTop: 6 }}>
                {t("sessionMaintenance.lockResult", {
                  removed: control.lockResult.removed?.length ?? 0,
                  failed: control.lockResult.failed?.length ?? 0,
                })}
              </div>
            ) : null}
          </section>
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
