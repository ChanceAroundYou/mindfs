// 控制面的一次性提示：主控离线 / 恢复、以及同步冲突提醒。
//
// 只在「模式迁移」或「新冲突事件」时出现，点关闭或处理后消失；常驻状态在左侧
// 三点菜单里（ControlModeMenuSection）。这里不轮询 —— 状态由 ControlSyncProvider
// 那唯一一处 useControlSyncStatus 持有，避免两处各自轮询给出不同结论。
import React from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../../i18n";
import type { ControlBannerAction } from "../../shared/controlMode";
import type { ControlSyncValue } from "../../app/ControlSyncProvider";

const TONE_COLOR: Record<string, string> = {
  online: "var(--text-secondary, #888)",
  local: "var(--status-warn)",
  syncing: "var(--accent-color)",
  conflict: "var(--status-bad)",
};

const STACK_STYLE: React.CSSProperties = {
  position: "fixed",
  top: 8,
  left: "50%",
  transform: "translateX(-50%)",
  zIndex: 1000,
  display: "flex",
  flexDirection: "column",
  gap: 6,
  width: "max-content",
  maxWidth: "min(560px, calc(100vw - 24px))",
  pointerEvents: "none",
};

const ROW_STYLE: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 8,
  flexWrap: "wrap",
  padding: "7px 10px",
  borderRadius: 10,
  fontSize: 12,
  lineHeight: 1.4,
  color: "var(--text-primary, inherit)",
  pointerEvents: "auto",
  background: "var(--panel-bg, #fff)",
  boxShadow: "0 6px 20px color-mix(in srgb, var(--text-primary, #000) 18%, transparent)",
};

const DOT_STYLE = (color: string): React.CSSProperties => ({
  width: 8,
  height: 8,
  borderRadius: "50%",
  background: color,
  flex: "0 0 auto",
});

const BUTTON_STYLE = (color: string, busy: boolean): React.CSSProperties => ({
  padding: "2px 8px",
  borderRadius: 7,
  border: `1px solid color-mix(in srgb, ${color} 50%, transparent)`,
  background: `color-mix(in srgb, ${color} 18%, transparent)`,
  color: "var(--text-primary, inherit)",
  font: "inherit",
  fontSize: 11,
  cursor: busy ? "default" : "pointer",
});

const ACTION_LABEL: Record<ControlBannerAction, "controlMode.retry" | "controlMode.switchLocal" | "controlMode.viewConflicts" | "controlMode.adoptPrimary"> = {
  retry: "controlMode.retry",
  "switch-local": "controlMode.switchLocal",
  "view-conflicts": "controlMode.viewConflicts",
  "adopt-primary": "controlMode.adoptPrimary",
};

export function ControlModeNotices({ control }: { control: ControlSyncValue }) {
  const { t } = useI18n();
  const { notice, reminder, status, busy, runAction, dismissNotice, dismissConflictReminder } = control;

  const rows: React.ReactNode[] = [];

  if (reminder) {
    const color = TONE_COLOR.conflict;
    rows.push(
      <div key={`conflict:${reminder.id}`} style={{ ...ROW_STYLE, border: `1px solid color-mix(in srgb, ${color} 45%, transparent)` }}>
        <span aria-hidden style={DOT_STYLE(color)} />
        <span style={{ color, minWidth: 0 }}>
          {t("controlMode.conflictReminder", { count: status?.conflict_count ?? 0 })}
        </span>
        <button
          type="button"
          onClick={() => { void runAction("view-conflicts"); }}
          disabled={busy}
          style={BUTTON_STYLE(color, busy)}
        >
          {t(ACTION_LABEL["view-conflicts"])}
        </button>
        <button type="button" onClick={dismissConflictReminder} style={BUTTON_STYLE(color, false)}>
          {t("controlMode.dismiss")}
        </button>
      </div>,
    );
  }

  if (notice) {
    const color = notice.kind === "offline" ? TONE_COLOR.local : TONE_COLOR.syncing;
    const textKey = notice.kind === "offline" ? "controlMode.offlineNotice" : "controlMode.recoveredNotice";
    const actionKey = notice.kind === "offline" ? "controlMode.retry" : "controlMode.syncNow";
    rows.push(
      <div key={notice.id} style={{ ...ROW_STYLE, border: `1px solid color-mix(in srgb, ${color} 45%, transparent)` }}>
        <span aria-hidden style={DOT_STYLE(color)} />
        <span style={{ minWidth: 0 }}>{t(textKey)}</span>
        <button
          type="button"
          onClick={() => { dismissNotice(); void runAction(notice.action); }}
          disabled={busy}
          style={BUTTON_STYLE(color, busy)}
        >
          {t(actionKey)}
        </button>
        <button type="button" onClick={dismissNotice} style={BUTTON_STYLE(color, false)}>
          {t("controlMode.dismiss")}
        </button>
      </div>,
    );
  }

  if (rows.length === 0) return null;
  return createPortal(<div style={STACK_STYLE}>{rows}</div>, document.body);
}
