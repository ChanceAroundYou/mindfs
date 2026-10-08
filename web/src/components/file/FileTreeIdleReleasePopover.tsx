import React from "react";

export function FileTreeIdleReleasePopover({ open, hours, busy, error, setHours, close, save, t, rowStyle, secondaryStyle, primaryStyle }: any) {
  if (!open) return null;
  const idleReleaseHours = hours;
  const idleReleaseBusy = busy;
  const idleReleaseError = error;
  const setIdleReleaseHours = setHours;
  const setIdleReleaseOpen = close;
  const saveIdleSessionResourceRelease = save;
  const agentConfigSecondaryButtonStyle = secondaryStyle;
  const agentConfigActionRowStyle = rowStyle;
  const agentConfigPrimaryButtonStyle = primaryStyle;
  return (<div
  style={{
    position: "absolute",
    top: "calc(100% + 6px)",
    left: "8px",
    right: "3px",
    zIndex: 40,
    padding: "14px",
    borderRadius: "12px",
    border: "1px solid var(--border-color)",
    background: "var(--menu-bg)",
    boxShadow: "0 16px 36px rgba(15, 23, 42, 0.18)",
  }}
>
  <div style={{ fontSize: "13px", fontWeight: 700, color: "var(--text-primary)" }}>
    {t("idleSessionResourceRelease.title")}
  </div>
  <div style={{ marginTop: "6px", fontSize: "11px", lineHeight: 1.5, color: "var(--text-secondary)" }}>
    {t("idleSessionResourceRelease.description")}
  </div>
  <label
    style={{
      marginTop: "12px",
      display: "flex",
      alignItems: "center",
      gap: "8px",
      color: "var(--text-primary)",
      fontSize: "12px",
    }}
  >
    <input
      type="number"
      min="1"
      step="1"
      inputMode="numeric"
      value={idleReleaseHours}
      disabled={idleReleaseBusy}
      onChange={(event) => setIdleReleaseHours(event.target.value)}
      onKeyDown={(event) => {
        if (event.key === "Enter") {
          event.preventDefault();
          void saveIdleSessionResourceRelease();
        }
      }}
      style={{
        width: "96px",
        boxSizing: "border-box",
        padding: "8px 10px",
        borderRadius: "8px",
        border: "1px solid var(--border-color)",
        background: "var(--content-bg)",
        color: "var(--text-primary)",
        outline: "none",
      }}
    />
    <span>{t("idleSessionResourceRelease.hours")}</span>
  </label>
  {idleReleaseError ? (
    <div style={{ marginTop: "8px", color: "#dc2626", fontSize: "11px", lineHeight: 1.4 }}>
      {idleReleaseError}
    </div>
  ) : null}
  <div style={{ ...agentConfigActionRowStyle, marginTop: "12px" }}>
    <button
      type="button"
      disabled={idleReleaseBusy}
      onClick={() => setIdleReleaseOpen(false)}
      style={agentConfigSecondaryButtonStyle(idleReleaseBusy)}
    >
      {t("common.cancel")}
    </button>
    <button
      type="button"
      disabled={idleReleaseBusy}
      onClick={() => void saveIdleSessionResourceRelease()}
      style={agentConfigPrimaryButtonStyle(idleReleaseBusy)}
    >
      {idleReleaseBusy ? t("common.saving") : t("common.save")}
    </button>
  </div>
  </div>
  );
}
