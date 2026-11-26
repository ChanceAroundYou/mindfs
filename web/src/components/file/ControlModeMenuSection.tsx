// 左侧三点菜单里的常驻控制面状态。
//
// 页面顶部不再有固定横幅；状态收在这里，折叠时只显示色点 + 模式名，展开后才显示
// 最近同步时间、诊断与事件历史。动作仍走 ControlSyncProvider 的那一次轮询。
import React, { useEffect, useState } from "react";
import { useI18n } from "../../i18n";
import { controlModeLabel, type ControlBannerAction } from "../../shared/controlMode";
import { useControlSync } from "../../app/ControlSyncProvider";

const TONE_COLOR: Record<string, string> = {
  online: "var(--text-secondary, #888)",
  local: "var(--status-warn)",
  syncing: "var(--accent-color)",
  conflict: "var(--status-bad)",
};

const SECTION_STYLE: React.CSSProperties = {
  borderTop: "1px solid var(--border-color)",
  marginTop: 4,
  paddingTop: 4,
};

const ROW_STYLE: React.CSSProperties = {
  width: "100%",
  display: "flex",
  alignItems: "center",
  gap: 6,
  padding: "6px 10px",
  border: 0,
  background: "transparent",
  color: "var(--text-primary, inherit)",
  font: "inherit",
  fontSize: 12,
  cursor: "pointer",
  textAlign: "left",
};

const DOT_STYLE = (color: string): React.CSSProperties => ({
  width: 7,
  height: 7,
  borderRadius: "50%",
  background: color,
  flex: "0 0 auto",
});

const ACTION_STYLE = (color: string): React.CSSProperties => ({
  padding: "2px 7px",
  borderRadius: 6,
  border: `1px solid color-mix(in srgb, ${color} 50%, transparent)`,
  background: `color-mix(in srgb, ${color} 16%, transparent)`,
  color: "var(--text-primary, inherit)",
  font: "inherit",
  fontSize: 11,
  cursor: "pointer",
});

const ACTION_LABEL: Record<ControlBannerAction, "controlMode.retry" | "controlMode.switchLocal" | "controlMode.viewConflicts" | "controlMode.adoptPrimary"> = {
  retry: "controlMode.retry",
  "switch-local": "controlMode.switchLocal",
  "view-conflicts": "controlMode.viewConflicts",
  "adopt-primary": "controlMode.adoptPrimary",
};

export function ControlModeMenuSection() {
  const { t, formatTime } = useI18n();
  const control = useControlSync();
  const [expanded, setExpanded] = useState(false);
  // 「接受新主节点」是两步：先展开确认条，明写会丢什么，再点一次才真发请求。
  const [confirmAdopt, setConfirmAdopt] = useState(false);

  const status = control?.status ?? null;
  const banner = control?.banner ?? null;
  const modeKey = status?.mode ?? "";
  useEffect(() => {
    setConfirmAdopt(false);
  }, [modeKey]);

  if (!control?.enabled || !banner) return null;

  const color = TONE_COLOR[banner.tone];
  const time = status?.last_primary_at ? formatTime(status.last_primary_at) : "—";
  const detail = t(banner.detail, { time });
  const diagnostics = [status?.blocked_reason, status?.last_error].filter(Boolean) as string[];
  const events = (status?.recent_events ?? []).slice(-3).reverse();

  const run = (action: ControlBannerAction) => {
    if (action === "adopt-primary" && !confirmAdopt) {
      setConfirmAdopt(true);
      return;
    }
    setConfirmAdopt(false);
    void control.runAction(action);
  };

  return (
    <div style={SECTION_STYLE}>
      <button
        type="button"
        onClick={() => setExpanded((value) => !value)}
        aria-expanded={expanded}
        title={detail}
        style={ROW_STYLE}
      >
        <span aria-hidden style={DOT_STYLE(color)} />
        <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {t(banner.title)}
        </span>
        <span style={{ color: "var(--text-secondary)", fontSize: 10 }}>{expanded ? "▴" : "▾"}</span>
      </button>
      {expanded ? (
        <div style={{ display: "flex", flexDirection: "column", gap: 6, padding: "2px 10px 6px" }}>
          <span style={{ color: "var(--text-secondary)", fontSize: 11, lineHeight: 1.4 }}>{detail}</span>
          {diagnostics.length > 0 ? (
            <span style={{ color, fontSize: 11, overflowWrap: "anywhere" }}>{diagnostics.join(" · ")}</span>
          ) : null}
          {events.length > 0 ? (
            <span style={{ display: "flex", flexDirection: "column", gap: 2, fontSize: 10, color: "var(--text-secondary)" }}>
              {events.map((event) => (
                <span key={event.id} title={event.reason || undefined}>
                  {formatTime(event.at)} · {t(controlModeLabel(event.to))}
                </span>
              ))}
            </span>
          ) : null}
          <span style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
            {banner.actions.map((action) =>
              action === "adopt-primary" && confirmAdopt ? null : (
                <button
                  key={action}
                  type="button"
                  onClick={() => run(action)}
                  disabled={control.busy}
                  style={{ ...ACTION_STYLE(color), cursor: control.busy ? "default" : "pointer" }}
                >
                  {t(ACTION_LABEL[action])}
                </button>
              ),
            )}
          </span>
          {confirmAdopt ? (
            <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
              <span style={{ color, fontSize: 10, lineHeight: 1.4, overflowWrap: "anywhere" }}>
                {t("controlMode.adoptWarning")}
              </span>
              <span style={{ display: "flex", gap: 6 }}>
                <button
                  type="button"
                  onClick={() => { setConfirmAdopt(false); void control.runAction("adopt-primary"); }}
                  disabled={control.busy}
                  style={ACTION_STYLE(color)}
                >
                  {t("controlMode.adoptConfirm")}
                </button>
                <button type="button" onClick={() => setConfirmAdopt(false)} style={ACTION_STYLE(color)}>
                  {t("controlMode.cancel")}
                </button>
              </span>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
