import React from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../../i18n/index";
import { useContextSaving, type ContextSavingIndicatorProps } from "./useContextSaving";
import { COMPOSER_CHIP_FONT_SIZE } from "../action/composerStyles";

function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "0";
  const mib = value / (1024 ** 2);
  if (mib >= 1) return `${mib.toFixed(mib >= 10 ? 0 : 1)} MB`;
  return `${Math.round(value / 1024)} KB`;
}

// Group digits: 408625 is measurably harder to read at a glance than 408,625,
// and token totals here routinely run into six figures.
function formatTokens(value: number): string {
  if (!Number.isFinite(value) || value < 0) return "0";
  return Math.round(value).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

// The chip header has room for a token count, not six digits plus separators.
// Raw values stay in the detail rows, where there is space to be exact.
function formatTokensCompact(value: number): string {
  if (!Number.isFinite(value) || value < 0) return "0";
  const rounded = Math.round(value);
  if (rounded < 1000) return String(rounded);
  const thousands = rounded / 1000;
  return thousands >= 100 ? `${Math.round(thousands)}k` : `${thousands.toFixed(1)}k`;
}

// Share of the original request saved by folding. One decimal below 10% keeps
// small savings visible ("5.7%") without the noise of false precision above it.
function formatPercent(saved: number, original: number): string | null {
  if (!Number.isFinite(saved) || !Number.isFinite(original) || saved < 0 || original <= 0) return null;
  const percent = (saved * 100) / original;
  if (!Number.isFinite(percent)) return null;
  return `${percent.toFixed(percent < 10 ? 1 : 0)}%`;
}

/**
 * 会话页的「上下文」胶囊：这一轮实际发出的请求比 CLI 原本要发的小多少。
 *
 * 只在 live-compact 真的折叠过（有账本记录）时出现；未配置 / 未折叠的节点
 * 什么都不显示，而不是挂一个恒为 0 的徽标。
 */
export function ContextSavingIndicator(props: ContextSavingIndicatorProps) {
  const { t } = useI18n();
  const { saving, open, loading, error, savedBytes, visible, setOpen, handleOpen } = useContextSaving(props);

  if (!visible) return null;

  const latest = saving?.latest;
  // One unit throughout: tokens are what the request costs, and the ledger now
  // carries the same two bodies measured with the kernel's estimator. Rows
  // written before that field existed have no token count, so those fall back
  // to bytes — bytes/4 is not a substitute (it is off by ~2x on CJK).
  const tokenSized = !!latest && latest.req_tokens_est > 0 && latest.forward_tokens_est > 0;
  const savedTokens = tokenSized ? latest.req_tokens_est - latest.forward_tokens_est : 0;
  const savedPercent = latest
    ? formatPercent(tokenSized ? savedTokens : latest.saved_bytes, tokenSized ? latest.req_tokens_est : latest.req_bytes)
    : null;
  const rows: Array<[string, string]> = [];
  if (latest) {
    const savedText = tokenSized
      ? t("contextSaving.tokensValue", { count: formatTokens(savedTokens) })
      : formatBytes(latest.saved_bytes);
    rows.push(
      [
        t("contextSaving.badgeRequest"),
        tokenSized
          ? t("contextSaving.requestTokensValue", {
              req: formatTokens(latest.req_tokens_est),
              forwarded: formatTokens(latest.forward_tokens_est),
            })
          : `${formatBytes(latest.req_bytes)} → ${formatBytes(latest.folded_bytes)}`,
      ],
      [
        t("contextSaving.badgeSaved"),
        savedPercent ? t("contextSaving.savedValue", { size: savedText, percent: savedPercent }) : savedText,
      ],
      [t("contextSaving.badgeBlocks"), t("contextSaving.blocksValue", { count: String(latest.blocks) })],
      [t("contextSaving.badgeTokens"), t("contextSaving.tokensValue", { count: formatTokens(latest.tokens_est) })],
      [
        t("contextSaving.badgeRewrite"),
        latest.usage_source === "missing" || latest.rewrite_tokens < 0
          ? t("contextSaving.unknown")
          : t("contextSaving.tokensValue", { count: formatTokens(latest.rewrite_tokens) }),
      ],
    );
  }
  const savedLabel = tokenSized
    ? t("contextSaving.tokensValue", { count: formatTokensCompact(savedTokens) })
    : formatBytes(savedBytes);
  const savedTitle = tokenSized
    ? t("contextSaving.tokensValue", { count: formatTokens(savedTokens) })
    : formatBytes(savedBytes);

  const modal = open && typeof document !== "undefined" ? createPortal(
    <div
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) setOpen(false);
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
        aria-labelledby="context-saving-title"
        style={{
          width: "min(440px, calc(100vw - 32px))",
          maxHeight: "min(640px, calc(100vh - 40px))",
          overflowY: "auto",
          borderRadius: "16px",
          border: "1px solid var(--panel-border)",
          background: "var(--panel-bg)",
          boxShadow: "0 24px 70px rgba(15, 23, 42, 0.28)",
          padding: "20px",
          boxSizing: "border-box",
        }}
      >
        <h2 id="context-saving-title" style={{ margin: 0, fontSize: "16px", lineHeight: 1.35, color: "var(--text-primary)" }}>
          {t("contextSaving.title")}
        </h2>
        <p style={{ margin: "7px 0 0", fontSize: "12px", lineHeight: 1.6, color: "var(--text-secondary)" }}>
          {t("contextSaving.description")}
        </p>
        <div style={{ marginTop: "14px", border: "1px solid var(--border-color)", borderRadius: "10px", overflow: "hidden" }}>
          {loading && !latest ? (
            <div style={{ padding: "18px 10px", textAlign: "center", color: "var(--text-secondary)", fontSize: "12px" }}>{t("common.loading")}</div>
          ) : rows.length === 0 ? (
            <div style={{ padding: "18px 10px", textAlign: "center", color: "var(--text-secondary)", fontSize: "12px" }}>{t("contextSaving.empty")}</div>
          ) : rows.map(([label, value], index) => (
            <div key={label} style={{ display: "flex", justifyContent: "space-between", gap: "12px", padding: "9px 12px", borderTop: index === 0 ? "none" : "1px solid var(--border-color)", color: "var(--text-primary)", fontSize: "12px" }}>
              <span style={{ color: "var(--text-secondary)" }}>{label}</span>
              <span style={{ fontVariantNumeric: "tabular-nums", fontWeight: 650 }}>{value}</span>
            </div>
          ))}
        </div>
        {saving && saving.usage_missing > 0 ? (
          <div style={{ marginTop: "8px", padding: "10px 12px", borderRadius: "10px", background: "color-mix(in srgb, var(--accent-color) 8%, transparent)", color: "var(--text-secondary)", fontSize: "12px", lineHeight: 1.6 }}>
            {t("contextSaving.usageMissing", { count: String(saving.usage_missing) })}
          </div>
        ) : null}
        {saving && saving.total_rewrite_tokens > 0 ? (
          <div style={{ marginTop: "8px", padding: "10px 12px", borderRadius: "10px", background: "color-mix(in srgb, var(--accent-color) 8%, transparent)", color: "var(--text-secondary)", fontSize: "12px", lineHeight: 1.6 }}>
            {t("contextSaving.rewriteTotal", { rewrite: formatTokens(saving.total_rewrite_tokens) })}
          </div>
        ) : null}
        {error ? <div style={{ marginTop: "10px", padding: "9px 11px", borderRadius: "9px", background: "rgba(220, 38, 38, 0.09)", color: "#dc2626", fontSize: "12px", overflowWrap: "anywhere" }}>{error}</div> : null}
        <div style={{ display: "flex", justifyContent: "flex-end", marginTop: "18px" }}>
          <button type="button" onClick={() => setOpen(false)} style={{ height: "34px", padding: "0 14px", borderRadius: "9px", border: "1px solid var(--panel-border)", background: "transparent", color: "var(--text-primary)", cursor: "pointer", fontWeight: 650 }}>{t("common.close")}</button>
        </div>
      </div>
    </div>,
    document.body,
  ) : null;

  return (
    <>
      <button
        type="button"
        onClick={handleOpen}
        title={t("contextSaving.badgeTitle", { saved: savedTitle })}
        aria-label={t("contextSaving.badgeTitle", { saved: savedTitle })}
        style={{ display: "inline-flex", alignItems: "center", height: "20px", padding: "0 8px", borderRadius: "999px", border: "1px solid rgba(15, 118, 110, 0.24)", background: "linear-gradient(rgba(15, 118, 110, 0.09), rgba(15, 118, 110, 0.09)), var(--mobile-overlay-bg)", color: "#0f766e", fontSize: COMPOSER_CHIP_FONT_SIZE, fontWeight: 700, lineHeight: 1, whiteSpace: "nowrap", cursor: "pointer" }}
      >
        {t("contextSaving.badge", { saved: savedLabel })}
      </button>
      {modal}
    </>
  );
}
