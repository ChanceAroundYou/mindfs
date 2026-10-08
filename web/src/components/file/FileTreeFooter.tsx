import React from "react";

export function FileTreeFooter(props: any) {
  const { hasFooterContent, updateNotesRef, updateActionLabel, updateActionSummary, updateActionDisabled, updateActionBusy, updateActionHelp, isUpdateNotesOpen, setIsUpdateNotesOpen, onUpdateAction, footerTopContent, isNativeApp, onGoHome, shouldShowInstallButton, handleInstall, installLabel, shouldShowInstallHelp, installHelp, t } = props;
  return (
      <div
data-mindfs-filetree-footer="1"
style={{
  padding: hasFooterContent ? "10px 12px 12px" : "0",
  borderTop: hasFooterContent ? "1px solid var(--border-color)" : "none",
  display: "flex",
  flexDirection: "column",
  gap: "6px",
  flexShrink: 0,
}}
      >
{updateActionLabel ? (
  <div ref={updateNotesRef} style={{ position: "relative" }}>
    {isUpdateNotesOpen && updateActionSummary ? (
      <div
        style={{
          position: "absolute",
          left: 0,
          right: 0,
          bottom: "calc(100% + 8px)",
          border: "1px solid var(--border-color)",
          background: "var(--panel-bg)",
          borderRadius: "12px",
          padding: "12px",
          boxShadow: "0 18px 36px rgba(15, 23, 42, 0.18)",
          fontSize: "11px",
          color: "var(--text-secondary)",
          lineHeight: 1.55,
          whiteSpace: "pre-wrap",
          wordBreak: "break-word",
          maxHeight: "220px",
          overflow: "auto",
          zIndex: 10,
        }}
      >
        {updateActionSummary}
      </div>
    ) : null}
    <div
      style={{
        display: "flex",
        alignItems: "stretch",
        width: "100%",
        border: "1px solid var(--border-color)",
        background: updateActionDisabled ? "rgba(148, 163, 184, 0.2)" : "var(--accent-color)",
        color: updateActionDisabled ? "var(--text-secondary)" : "#fff",
        borderRadius: "10px",
        overflow: "hidden",
      }}
    >
      <button
        type="button"
        disabled={updateActionDisabled}
        onClick={() => onUpdateAction?.()}
        title={updateActionHelp || undefined}
        style={{
          flex: 1,
          border: "none",
          background: "transparent",
          color: "inherit",
          padding: "10px 12px",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          gap: "8px",
          cursor: updateActionDisabled ? "not-allowed" : "pointer",
          fontSize: "12px",
          fontWeight: 600,
        }}
      >
        {updateActionBusy ? (
          <span
            style={{
              width: "12px",
              height: "12px",
              borderRadius: "50%",
              border: "2px solid currentColor",
              borderRightColor: "transparent",
              display: "inline-block",
              animation: "mindfs-update-spin 0.9s linear infinite",
            }}
          />
        ) : null}
        <span>{updateActionLabel}</span>
      </button>
      {updateActionSummary ? (
        <button
          type="button"
          aria-label={isUpdateNotesOpen ? t("fileTree.hideUpdateNotes") : t("fileTree.showUpdateNotes")}
          aria-expanded={isUpdateNotesOpen}
          onClick={() => setIsUpdateNotesOpen((open: boolean) => !open)}
          style={{
            width: "34px",
            border: "none",
            background: "transparent",
            color: "inherit",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            cursor: "pointer",
            flexShrink: 0,
          }}
        >
          <svg
            width="12"
            height="12"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2.2"
            strokeLinecap="round"
            strokeLinejoin="round"
            style={{
              transform: isUpdateNotesOpen ? "rotate(180deg)" : "rotate(0deg)",
              transition: "transform 0.15s ease",
            }}
          >
            <polyline points="6 15 12 9 18 15" />
          </svg>
        </button>
      ) : null}
    </div>
  </div>
) : null}
{updateActionHelp && !updateActionLabel ? (
  <div style={{ fontSize: "11px", color: "var(--text-secondary)", lineHeight: 1.5, textAlign: "center" }}>
    {updateActionHelp}
  </div>
) : null}
{footerTopContent ? (
  <div style={{ width: "100%" }}>{footerTopContent}</div>
) : null}
{isNativeApp && onGoHome ? (
  <button
    type="button"
    onClick={() => onGoHome()}
    style={{
      width: "100%",
      border: "1px solid var(--border-color)",
      background: "var(--text-primary)",
      color: "var(--sidebar-bg)",
      borderRadius: "10px",
      padding: "10px 12px",
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
      gap: "8px",
      cursor: "pointer",
      fontSize: "12px",
      fontWeight: 600,
    }}
  >
    <span>{t("fileTree.goHome")}</span>
  </button>
) : null}
{shouldShowInstallButton ? (
  <button
    type="button"
    onClick={() => { void handleInstall(); }}
    style={{
      width: "100%",
      border:
        "1px solid color-mix(in srgb, var(--accent-color) 72%, var(--border-color))",
      background: "var(--accent-color)",
      color: "#fff",
      borderRadius: "10px",
      padding: "10px 12px",
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
      gap: "8px",
      cursor: "pointer",
      fontSize: "12px",
      fontWeight: 600,
      transition: "all 0.15s ease",
    }}
  >
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.1" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M12 16V4" />
      <path d="m7 9 5-5 5 5" />
      <path d="M20 16.5v1.5A2 2 0 0 1 18 20H6a2 2 0 0 1-2-2v-1.5" />
    </svg>
    <span>{installLabel}</span>
  </button>
) : null}
{shouldShowInstallHelp ? (
  <div style={{ fontSize: "11px", color: "var(--text-secondary)", lineHeight: 1.5, textAlign: "center" }}>
    {installHelp}
  </div>
) : null}
      </div>
  );
}
