// 控制面同步的全局横幅：四种模式都可见，在线时弱化。
//
// 挂到 document.body：主区有 contain: paint，任何 fixed 浮层留在 App 树里都会被
// 侧栏宽度顶偏（见 memory `mindfs-fixed-overlay-must-portal`）。
//
// 这里只做展示与点击：轮询与请求在 useControlSyncStatus，模式真相由服务端状态机
// 持有并持久化，前端不自行判定。事件历史也来自服务端（/status 的 recent_events），
// 换台浏览器看到的仍是同一份「什么时候变的」。
import React, { useState } from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../../i18n";
import { useControlSyncStatus } from "../../hooks/useControlSyncStatus";
import { controlModeLabel, type ControlBannerAction, type ControlModeBannerState } from "../../shared/controlMode";

const TONE_COLOR: Record<ControlModeBannerState["tone"], string> = {
  online: "var(--text-secondary, #888)",
  local: "var(--status-warn)",
  syncing: "var(--accent-color)",
  conflict: "var(--status-bad)",
};

export function ControlModeBanner() {
  const { t, formatTime } = useI18n();
  const { status, enabled, busy, banner, reminder, runAction, dismissConflictReminder } =
    useControlSyncStatus();
  const [expanded, setExpanded] = useState(false);

  if (!enabled || !banner) return null;

  const color = TONE_COLOR[banner.tone];
  // 在线是「一切正常」，弱化到低对比度且不带阴影；真出事的那三种照常抢眼。
  const subdued = banner.tone === "online";
  const detail = t(banner.detail, {
    time: status?.last_primary_at ? formatTime(status.last_primary_at) : "—",
  });
  const diagnostics = [status?.blocked_reason, status?.last_error].filter(Boolean);
  const events = status?.recent_events?.slice(0, 3) ?? [];

  return createPortal(
    <div
      role="status"
      style={{
        position: "fixed",
        top: 8,
        left: "50%",
        transform: "translateX(-50%)",
        zIndex: 900,
        display: "flex",
        flexWrap: "wrap",
        alignItems: "center",
        gap: 10,
        maxWidth: "min(92vw, 760px)",
        padding: "8px 12px",
        borderRadius: 10,
        border: `1px solid color-mix(in srgb, ${color} ${subdued ? 25 : 45}%, transparent)`,
        background: `color-mix(in srgb, ${color} ${subdued ? 6 : 12}%, var(--panel-bg))`,
        boxShadow: subdued ? "none" : "0 6px 20px rgba(0, 0, 0, 0.16)",
        opacity: subdued ? 0.85 : 1,
        fontSize: 13,
        lineHeight: 1.35,
      }}
    >
      <span
        aria-hidden="true"
        style={{
          width: 8,
          height: 8,
          borderRadius: "50%",
          background: color,
          flex: "0 0 auto",
        }}
      />
      <button
        type="button"
        onClick={() => setExpanded((value) => !value)}
        aria-expanded={expanded}
        title={detail}
        style={{
          border: 0,
          background: "transparent",
          padding: 0,
          color: "var(--text-primary, inherit)",
          font: "inherit",
          fontWeight: 600,
          cursor: "pointer",
          textAlign: "left",
        }}
      >
        {t(banner.title)}
      </button>
      <span style={{ color: "var(--text-secondary, inherit)", minWidth: 0 }}>{detail}</span>
      {expanded && diagnostics.length > 0 ? (
        <span style={{ color: color, minWidth: 0, overflowWrap: "anywhere", flexBasis: "100%" }}>
          {diagnostics.join(" · ")}
        </span>
      ) : null}
      {expanded && events.length > 0 ? (
        <span
          style={{
            color: "var(--text-secondary, inherit)",
            flexBasis: "100%",
            display: "flex",
            flexWrap: "wrap",
            gap: 8,
          }}
        >
          {events.map((event) => (
            <span key={event.id} title={event.reason || undefined}>
              {formatTime(event.at)} · {t(controlModeLabel(event.to))}
            </span>
          ))}
        </span>
      ) : null}
      {reminder ? (
        <span style={{ color: color, minWidth: 0 }}>
          {t("controlMode.conflictReminder", { count: status?.conflict_count ?? 0 })}
        </span>
      ) : null}
      {reminder ? (
        <button type="button" onClick={dismissConflictReminder} style={bannerButtonStyle(color)}>
          {t("controlMode.dismiss")}
        </button>
      ) : null}
      {banner.actions.map((action) => (
        <button
          key={action}
          type="button"
          onClick={() => {
            void runAction(action);
          }}
          disabled={busy}
          style={bannerButtonStyle(color, busy)}
        >
          {actionLabel(action, busy, t)}
        </button>
      ))}
    </div>,
    document.body,
  );
}

function actionLabel(
  action: ControlBannerAction,
  busy: boolean,
  t: ReturnType<typeof useI18n>["t"],
): string {
  if (action === "retry") return t(busy ? "controlMode.retrying" : "controlMode.retry");
  if (action === "switch-local") return t("controlMode.switchLocal");
  return t("controlMode.viewConflicts");
}

function bannerButtonStyle(color: string, busy = false): React.CSSProperties {
  return {
    flex: "0 0 auto",
    padding: "3px 10px",
    borderRadius: 7,
    border: `1px solid color-mix(in srgb, ${color} 50%, transparent)`,
    background: `color-mix(in srgb, ${color} 18%, transparent)`,
    color: "var(--text-primary, inherit)",
    font: "inherit",
    cursor: busy ? "default" : "pointer",
  };
}