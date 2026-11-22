// 控制面同步的全局横幅：四态里只有三种降级状态可见（在线不出横幅）。
//
// 挂到 document.body：主区有 contain: paint，任何 fixed 浮层留在 App 树里都会被
// 侧栏宽度顶偏（见 memory `mindfs-fixed-overlay-must-portal`）。
//
// 这里只做展示与点击：轮询与请求在 useControlSyncStatus，模式真相由服务端状态机
// 持有并持久化，前端不自行判定。
import React, { useState } from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../../i18n";
import { useControlSyncStatus } from "../../hooks/useControlSyncStatus";
import type { ControlModeBannerState } from "../../shared/controlMode";

const TONE_COLOR: Record<ControlModeBannerState["tone"], string> = {
  local: "var(--status-warn)",
  syncing: "var(--accent-color)",
  conflict: "var(--status-bad)",
};

export function ControlModeBanner() {
  const { t, formatTime } = useI18n();
  const { status, enabled, busy, banner, runAction } = useControlSyncStatus();
  const [expanded, setExpanded] = useState(false);

  if (!enabled || !banner) return null;

  const color = TONE_COLOR[banner.tone];
  const actionLabel =
    banner.action === "retry"
      ? t(busy ? "controlMode.retrying" : "controlMode.retry")
      : banner.action === "switch-local"
        ? t("controlMode.switchLocal")
        : t("controlMode.viewConflicts");
  const detail = t(banner.detail, {
    time: status?.last_primary_at ? formatTime(status.last_primary_at) : "—",
  });

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
        alignItems: "center",
        gap: 10,
        maxWidth: "min(92vw, 760px)",
        padding: "8px 12px",
        borderRadius: 10,
        border: `1px solid color-mix(in srgb, ${color} 45%, transparent)`,
        background: `color-mix(in srgb, ${color} 12%, var(--panel-bg))`,
        boxShadow: "0 6px 20px rgba(0, 0, 0, 0.16)",
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
      {expanded && status?.last_error ? (
        <span style={{ color: color, minWidth: 0, overflowWrap: "anywhere" }}>
          {status.last_error}
        </span>
      ) : null}
      <button
        type="button"
        onClick={() => {
          void runAction();
        }}
        disabled={busy}
        style={{
          flex: "0 0 auto",
          padding: "3px 10px",
          borderRadius: 7,
          border: `1px solid color-mix(in srgb, ${color} 50%, transparent)`,
          background: `color-mix(in srgb, ${color} 18%, transparent)`,
          color: "var(--text-primary, inherit)",
          font: "inherit",
          cursor: busy ? "default" : "pointer",
        }}
      >
        {actionLabel}
      </button>
    </div>,
    document.body,
  );
}