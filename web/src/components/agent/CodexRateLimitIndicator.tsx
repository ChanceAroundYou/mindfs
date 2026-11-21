import React from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../../i18n/index";
import { useCodexRateLimit, type CodexRateLimitIndicatorProps } from "./useCodexRateLimit";
import { COMPOSER_CHIP_FONT_SIZE } from "../action/composerStyles";

export function CodexRateLimitIndicator(props: CodexRateLimitIndicatorProps) {
  const { agent } = props;
  const { t } = useI18n();
  const {
    status,
    loading,
    confirmOpen,
    detailsOpen,
    resetting,
    resetOutcome,
    error,
    limits,
    tightestLimit,
    resetCount,
    canReset,
    firstCredit,
    setConfirmOpen,
    setDetailsOpen,
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

  // 两条限额收成一个胶囊：只显示剩余最少的窗口，点开才展开完整详情。
  const chipPercent = tightestLimit
    ? tightestLimit.remaining === null
      ? (loading ? "··" : "—")
      : `${tightestLimit.remaining}%`
    : "";
  const chipSummary = tightestLimit
    ? tightestLimit.resetText
      ? t("codexLimit.limitValueWithReset", { name: tightestLimit.label, percent: chipPercent, time: tightestLimit.resetText })
      : t("codexLimit.limitValue", { name: tightestLimit.label, percent: chipPercent })
    : "";
  const chipTitle = tightestLimit
    ? tightestLimit.resetText
      ? t("codexLimit.limitTitle", { name: tightestLimit.label, percent: chipPercent, time: tightestLimit.resetText })
      : chipSummary
    : "";
  const chipHint = t("codexLimit.badgeTitle", { summary: chipTitle });

  const details = detailsOpen && typeof document !== "undefined" ? createPortal(
    <div
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) setDetailsOpen(false);
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
        aria-labelledby="codex-limit-details-title"
        style={{
          width: "min(390px, calc(100vw - 32px))",
          borderRadius: "16px",
          border: "1px solid var(--panel-border)",
          background: "var(--panel-bg)",
          boxShadow: "0 24px 70px rgba(15, 23, 42, 0.28)",
          padding: "20px",
        }}
      >
        <h2 id="codex-limit-details-title" style={{ margin: 0, fontSize: "16px", lineHeight: 1.35, color: "var(--text-primary)" }}>{t("codexLimit.detailsTitle")}</h2>
        <p style={{ margin: "7px 0 0", fontSize: "13px", lineHeight: 1.6, color: "var(--text-secondary)" }}>{t("codexLimit.detailsDescription")}</p>
        <div style={{ marginTop: "14px", border: "1px solid var(--border-color)", borderRadius: "10px", overflow: "hidden" }}>
          {limits.length === 0 ? (
            <div style={{ padding: "18px 10px", textAlign: "center", color: "var(--text-secondary)", fontSize: "12px" }}>
              {loading ? t("common.loading") : t("codexLimit.detailsEmpty")}
            </div>
          ) : limits.map((limit, index) => {
            const percent = limit.remaining === null ? (loading ? "··" : "—") : `${limit.remaining}%`;
            return (
              <div key={limit.id} style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start", gap: "12px", padding: "10px 12px", borderTop: index === 0 ? "none" : "1px solid var(--border-color)", fontSize: "12px" }}>
                <span style={{ color: "var(--text-secondary)", fontWeight: 650, lineHeight: 1.5 }}>{limit.label}</span>
                <span style={{ display: "flex", flexDirection: "column", alignItems: "flex-end", textAlign: "right", lineHeight: 1.5 }}>
                  <span style={{ color: "var(--text-primary)", fontVariantNumeric: "tabular-nums", fontWeight: 650 }}>{t("codexLimit.detailsRemaining", { percent })}</span>
                  {limit.resetText ? (
                    <span style={{ color: "var(--text-secondary)", fontSize: "11px" }}>
                      {limit.id === "five_hour"
                        ? t("codexLimit.detailsResetAt", { time: limit.resetText })
                        : t("codexLimit.detailsResetIn", { time: limit.resetText })}
                    </span>
                  ) : null}
                </span>
              </div>
            );
          })}
          {status ? (
            <div style={{ display: "flex", justifyContent: "space-between", gap: "12px", padding: "10px 12px", borderTop: limits.length === 0 ? "none" : "1px solid var(--border-color)", color: "var(--text-primary)", fontSize: "12px" }}>
              <span style={{ color: "var(--text-secondary)" }}>{t("codexLimit.reset")}</span>
              <span style={{ fontVariantNumeric: "tabular-nums", fontWeight: 650 }}>{t("codexLimit.detailsResetCredits", { count: resetCount })}</span>
            </div>
          ) : null}
        </div>
        {error ? <div style={{ marginTop: "10px", padding: "9px 11px", borderRadius: "9px", background: "rgba(220, 38, 38, 0.09)", color: "#dc2626", fontSize: "12px", overflowWrap: "anywhere" }}>{error}</div> : null}
        <div style={{ display: "flex", justifyContent: "flex-end", marginTop: "18px" }}>
          <button type="button" onClick={() => setDetailsOpen(false)} autoFocus style={{ height: "34px", padding: "0 14px", borderRadius: "9px", border: "1px solid var(--panel-border)", background: "transparent", color: "var(--text-primary)", cursor: "pointer", fontWeight: 650 }}>{t("common.close")}</button>
        </div>
      </div>
    </div>,
    document.body,
  ) : null;

  return (
    <>
      <div style={{ display: "inline-flex", flex: "0 0 auto", alignItems: "center", gap: "5px", height: "24px", minWidth: 0 }}>
        {tightestLimit ? (
          <button
            type="button"
            onClick={() => setDetailsOpen(true)}
            aria-haspopup="dialog"
            aria-expanded={detailsOpen}
            title={chipHint}
            aria-label={chipHint}
            style={{ display: "inline-flex", alignItems: "center", height: "20px", padding: "0 8px", borderRadius: "999px", border: "1px solid color-mix(in srgb, var(--accent-color) 22%, transparent)", background: "linear-gradient(color-mix(in srgb, var(--accent-color) 10%, transparent), color-mix(in srgb, var(--accent-color) 10%, transparent)), var(--mobile-overlay-bg)", color: "var(--accent-color)", fontSize: COMPOSER_CHIP_FONT_SIZE, fontWeight: 700, lineHeight: 1, whiteSpace: "nowrap", cursor: "pointer" }}
          >
            {chipSummary}
          </button>
        ) : null}
        {loading || resetCount > 0 ? (
          <button type="button" disabled={!canReset} onClick={() => { setResetOutcome(""); setError(""); setConfirmOpen(true); }} title={canReset ? t("codexLimit.resetAvailable", { count: resetCount }) : error || t("codexLimit.resetUnavailable")} aria-label={canReset ? t("codexLimit.resetAvailable", { count: resetCount }) : t("codexLimit.resetUnavailable")} style={{ display: "inline-flex", alignItems: "center", height: "20px", padding: "0 8px", borderRadius: "999px", border: canReset ? "1px solid rgba(180, 83, 9, 0.24)" : "1px solid var(--border-color)", background: canReset ? "linear-gradient(rgba(245, 158, 11, 0.10), rgba(245, 158, 11, 0.10)), var(--mobile-overlay-bg)" : "linear-gradient(rgba(100, 116, 139, 0.08), rgba(100, 116, 139, 0.08)), var(--mobile-overlay-bg)", color: canReset ? "#b45309" : "var(--text-secondary)", fontSize: COMPOSER_CHIP_FONT_SIZE, fontWeight: 700, lineHeight: 1, whiteSpace: "nowrap", cursor: canReset ? "pointer" : "default" }}>
            {t("codexLimit.reset")} {loading && !status ? "·" : resetCount}
          </button>
        ) : null}
      </div>
      {modal}
      {details}
    </>
  );
}
