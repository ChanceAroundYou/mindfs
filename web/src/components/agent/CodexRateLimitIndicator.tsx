import React from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../../i18n/index";
import { useCodexRateLimit, type CodexRateLimitIndicatorProps } from "./useCodexRateLimit";

export function CodexRateLimitIndicator(props: CodexRateLimitIndicatorProps) {
  const { agent } = props;
  const { t } = useI18n();
  const {
    status,
    loading,
    confirmOpen,
    resetting,
    resetOutcome,
    error,
    limits,
    resetCount,
    canReset,
    firstCredit,
    setConfirmOpen,
    setResetOutcome,
    setError,
    handleReset,
  } = useCodexRateLimit(props);

  if (agent !== "codex") return null;
  if (!status?.uses_chatgpt_plan) return null;

  const modal = confirmOpen && typeof document !== "undefined" ? createPortal(
    <div
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget && !resetting) setConfirmOpen(false);
      }}
      style={{
        position: "fixed",
        inset: 0,
        zIndex: 10050,
        display: "grid",
        placeItems: "center",
        padding: "20px",
        background: "rgba(15, 23, 42, 0.44)",
        backdropFilter: "blur(5px)",
      }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="codex-reset-title"
        style={{
          width: "min(390px, calc(100vw - 32px))",
          borderRadius: "16px",
          border: "1px solid var(--panel-border)",
          background: "var(--panel-bg)",
          boxShadow: "0 24px 70px rgba(15, 23, 42, 0.28)",
          padding: "20px",
        }}
      >
        <div>
          <div style={{ minWidth: 0 }}>
            <h2 id="codex-reset-title" style={{ margin: 0, fontSize: "16px", lineHeight: 1.35, color: "var(--text-primary)" }}>{t("codexLimit.confirmTitle")}</h2>
            <p style={{ margin: "7px 0 0", fontSize: "13px", lineHeight: 1.6, color: "var(--text-secondary)" }}>
              {t("codexLimit.confirmDescription", { count: resetCount })}
            </p>
            {firstCredit?.description ? <p style={{ margin: "8px 0 0", fontSize: "12px", lineHeight: 1.5, color: "var(--text-secondary)" }}>{firstCredit.description}</p> : null}
          </div>
        </div>
        {resetOutcome === "nothingToReset" ? <div style={{ marginTop: "14px", padding: "9px 11px", borderRadius: "9px", background: "rgba(245, 158, 11, 0.10)", color: "#b45309", fontSize: "12px" }}>{t("codexLimit.nothingToReset")}</div> : null}
        {resetOutcome === "noCredit" ? <div style={{ marginTop: "14px", padding: "9px 11px", borderRadius: "9px", background: "rgba(220, 38, 38, 0.09)", color: "#dc2626", fontSize: "12px" }}>{t("codexLimit.noCredit")}</div> : null}
        {error ? <div style={{ marginTop: "14px", padding: "9px 11px", borderRadius: "9px", background: "rgba(220, 38, 38, 0.09)", color: "#dc2626", fontSize: "12px", overflowWrap: "anywhere" }}>{error}</div> : null}
        <div style={{ display: "flex", justifyContent: "flex-end", gap: "8px", marginTop: "18px" }}>
          <button type="button" onClick={() => setConfirmOpen(false)} disabled={resetting} autoFocus style={{ height: "34px", padding: "0 14px", borderRadius: "9px", border: "1px solid var(--panel-border)", background: "transparent", color: "var(--text-primary)", cursor: resetting ? "not-allowed" : "pointer", fontWeight: 650 }}>{t("common.cancel")}</button>
          <button type="button" onClick={() => void handleReset()} disabled={resetting || !canReset} style={{ height: "34px", padding: "0 14px", borderRadius: "9px", border: "none", background: "#b45309", color: "#fff", cursor: resetting ? "wait" : "pointer", fontWeight: 750, opacity: resetting || !canReset ? 0.55 : 1 }}>{resetting ? t("codexLimit.resetting") : t("codexLimit.confirmAction")}</button>
        </div>
      </div>
    </div>,
    document.body,
  ) : null;

  return (
    <>
      <div style={{ display: "inline-flex", flex: "0 0 auto", alignItems: "center", gap: "5px", height: "24px", minWidth: 0 }}>
        {limits.map((limit) => {
          const percent = limit.remaining === null ? (loading ? "··" : "—") : `${limit.remaining}%`;
          const label = limit.resetTime
            ? t("codexLimit.limitValueWithReset", { name: limit.label, percent, time: limit.resetTime })
            : t("codexLimit.limitValue", { name: limit.label, percent });
          const title = limit.resetTime
            ? t("codexLimit.limitTitle", { name: limit.label, percent, time: limit.resetTime })
            : label;
          return (
            <div key={limit.id} title={title} style={{ display: "inline-flex", alignItems: "center", height: "20px", padding: "0 8px", borderRadius: "999px", border: "1px solid color-mix(in srgb, var(--accent-color) 22%, transparent)", background: "linear-gradient(color-mix(in srgb, var(--accent-color) 10%, transparent), color-mix(in srgb, var(--accent-color) 10%, transparent)), var(--mobile-overlay-bg)", color: "var(--accent-color)", fontSize: "11px", fontWeight: 700, lineHeight: 1, whiteSpace: "nowrap" }}>
              {label}
            </div>
          );
        })}
        <button type="button" disabled={!canReset} onClick={() => { setResetOutcome(""); setError(""); setConfirmOpen(true); }} title={canReset ? t("codexLimit.resetAvailable", { count: resetCount }) : error || t("codexLimit.resetUnavailable")} aria-label={canReset ? t("codexLimit.resetAvailable", { count: resetCount }) : t("codexLimit.resetUnavailable")} style={{ display: "inline-flex", alignItems: "center", height: "20px", padding: "0 8px", borderRadius: "999px", border: canReset ? "1px solid rgba(180, 83, 9, 0.24)" : "1px solid var(--border-color)", background: canReset ? "linear-gradient(rgba(245, 158, 11, 0.10), rgba(245, 158, 11, 0.10)), var(--mobile-overlay-bg)" : "linear-gradient(rgba(100, 116, 139, 0.08), rgba(100, 116, 139, 0.08)), var(--mobile-overlay-bg)", color: canReset ? "#b45309" : "var(--text-secondary)", fontSize: "11px", fontWeight: 700, lineHeight: 1, whiteSpace: "nowrap", cursor: canReset ? "pointer" : "default" }}>
          {t("codexLimit.reset")} {loading && !status ? "·" : resetCount}
        </button>
      </div>
      {modal}
    </>
  );
}
