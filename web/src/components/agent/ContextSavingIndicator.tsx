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

// Estimated token counts carry ±30%, so six digits is false precision: 118,460
// has no more information than 118k. Three significant digits, no trailing .0.
// Real usage numbers (rewrite, cache read) stay exact via formatTokens.
function formatTokensEstimate(value: number): string {
  if (!Number.isFinite(value) || value < 0) return "0";
  const rounded = Math.round(value);
  if (rounded < 1000) return String(rounded);
  const thousands = rounded / 1000;
  const text = thousands >= 100
    ? String(Math.round(thousands))
    : thousands >= 10
      ? thousands.toFixed(1)
      : thousands.toFixed(2);
  const trimmed = text.includes(".") ? text.replace(/0+$/, "").replace(/\.$/, "") : text;
  return `${trimmed}k`;
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
  const { saving, open, loading, error, savedBytes, hygiene, visible, compacting, setOpen, handleOpen, compact } = useContextSaving(props);

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
  // Folding breaks the prefix cache, so the rewrite cost only reads as worth
  // it next to the hit rate. usage_input is the fresh part, so the denominator
  // is all three disjoint counters; missing usage must not read as 0% hit.
  const cacheTotal = (latest?.usage_input || 0) + (latest?.usage_cache_creation || 0) + (latest?.usage_cache_read || 0);
  const cachePercent = latest && cacheTotal > 0 ? Math.round((latest.usage_cache_read * 100) / cacheTotal) : null;
  if (latest) {
    const savedText = tokenSized
      ? t("contextSaving.tokensValue", { count: formatTokens(savedTokens) })
      : formatBytes(latest.saved_bytes);
    rows.push(
      [
        t("contextSaving.badgeRequest"),
        tokenSized
          ? t("contextSaving.requestTokensValue", {
              req: formatTokensEstimate(latest.req_tokens_est),
              forwarded: formatTokensEstimate(latest.forward_tokens_est),
            })
          : `${formatBytes(latest.req_bytes)} → ${formatBytes(latest.folded_bytes)}`,
      ],
      [
        t("contextSaving.badgeSaved"),
        savedPercent ? t("contextSaving.savedValue", { size: savedText, percent: savedPercent }) : savedText,
      ],
    );
    // One turn's 5% is invisible; the session total is what the user is paying
    // to keep. The window is the retained ledger (summary.requests); with a
    // single row the total equals this turn and a second line adds nothing.
    if (saving && saving.requests > 1) {
      const totalText = saving.saved_token_samples > 0
        ? t("contextSaving.tokensValue", { count: formatTokens(saving.total_saved_tokens) })
        : formatBytes(saving.total_saved_bytes);
      rows.push([
        t("contextSaving.badgeSavedTotal"),
        t("contextSaving.savedTotalValue", { size: totalText, count: String(saving.requests) }),
      ]);
    }
    rows.push(
      [
        t("contextSaving.badgeBlocks"),
        latest.new_blocks > 0
          ? latest.new_tokens_est > 0
            ? t("contextSaving.blocksSplitTokensValue", { new: String(latest.new_blocks), tokens: formatTokensEstimate(latest.new_tokens_est), total: String(latest.blocks) })
            : t("contextSaving.blocksSplitValue", { new: String(latest.new_blocks), total: String(latest.blocks) })
          : t("contextSaving.blocksValue", { count: String(latest.blocks) }),
      ],
      [t("contextSaving.badgeTokens"), t("contextSaving.tokensValue", { count: formatTokensEstimate(latest.tokens_est) })],
      [
        t("contextSaving.badgeRewrite"),
        latest.usage_source === "missing" || latest.rewrite_tokens < 0
          ? t("contextSaving.unknown")
          : t("contextSaving.tokensValue", { count: formatTokens(latest.rewrite_tokens) }),
      ],
    );
    if (cachePercent !== null || latest.usage_source === "missing") {
      rows.push([
        t("contextSaving.badgeCacheHit"),
        cachePercent === null
          ? t("contextSaving.unknown")
          : t("contextSaving.cacheHitValue", { percent: `${cachePercent}%`, tokens: formatTokens(latest.usage_cache_read) }),
      ]);
    }
  }
  // The session total is a number, not a footnote: give it the same row shape
  // as every other figure so the panel stays a table, not prose.
  if (saving && saving.total_rewrite_tokens > 0) {
    rows.push([
      t("contextSaving.badgeRewriteTotal"),
      t("contextSaving.tokensValue", { count: formatTokens(saving.total_rewrite_tokens) }),
    ]);
  }
  // 超长会话提示：只大 + 只久 + 又久又大三种情况里，胶囊只在后两种出现。
  // 这几行与折叠省了多少无关，所以即使在完全没折叠过的会话里也要能读到。
  const hygieneBand = hygiene?.state === "hint" || hygiene?.state === "notice";
  const activeMinutes = Math.floor((hygiene?.active_seconds || 0) / 60);
  const hygieneTokens = formatTokensEstimate(hygiene?.context_tokens || 0);
  const turnsUntilAuto = hygiene?.turns_until_auto_compact ?? -1;
  if (hygieneBand && hygiene) {
    rows.push(
      [
        t("contextSaving.hygieneActive"),
        t("contextSaving.hygieneDuration", {
          hours: String(Math.floor(activeMinutes / 60)),
          minutes: String(activeMinutes % 60),
        }),
      ],
      [t("contextSaving.hygieneContext"), t("contextSaving.tokensValue", { count: hygieneTokens })],
    );
    if (hygiene.state === "hint" && turnsUntilAuto >= 0) {
      rows.push([t("contextSaving.hygieneAutoIn"), t("contextSaving.hygieneAutoInValue", { count: String(turnsUntilAuto) })]);
    }
  }
  // The chip has one line at 9px; the unit lives in the tooltip and the rows.
  const savedLabel = tokenSized ? formatTokensCompact(savedTokens) : formatBytes(savedBytes);
  const savedTitle = tokenSized
    ? t("contextSaving.tokensValue", { count: formatTokens(savedTokens) })
    : formatBytes(savedBytes);
  // 没折叠过但会话又大又久时，胶囊只承担提示：显示上下文规模，不再报 -0。
  const hasSaving = savedBytes > 0 || !!saving?.requests;
  const chipLabel = hasSaving
    ? t("contextSaving.badge", { saved: savedLabel })
    : t("contextSaving.badgeContext", { tokens: hygieneTokens });
  const chipTitle = hasSaving
    ? savedTitle
    : hygiene?.state === "hint"
      ? t("contextSaving.hygieneHintTitle", {
          tokens: hygieneTokens,
          count: String(Math.max(turnsUntilAuto, 0)),
        })
      : t("contextSaving.hygieneNoticeTitle", { tokens: hygieneTokens });

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
        {hygieneBand && hygiene ? (
          <div style={{ marginTop: "8px", padding: "10px 12px", borderRadius: "10px", background: "rgba(180, 83, 9, 0.08)", color: "var(--text-secondary)", fontSize: "12px", lineHeight: 1.6 }}>
            {hygiene.state === "hint"
              ? t("contextSaving.hygieneHintNotice", { count: String(Math.max(turnsUntilAuto, 0)) })
              : t("contextSaving.hygieneNoticeNotice")}
          </div>
        ) : null}
        {hygiene?.auto_failed ? (
          <div style={{ marginTop: "8px", padding: "10px 12px", borderRadius: "10px", background: "rgba(220, 38, 38, 0.08)", color: "#b91c1c", fontSize: "12px", lineHeight: 1.6 }}>
            {t("contextSaving.hygieneAutoFailed")}
          </div>
        ) : null}
        {error ? <div style={{ marginTop: "10px", padding: "9px 11px", borderRadius: "9px", background: "rgba(220, 38, 38, 0.09)", color: "#dc2626", fontSize: "12px", overflowWrap: "anywhere" }}>{error}</div> : null}
        <div style={{ display: "flex", justifyContent: "flex-end", gap: "8px", marginTop: "18px" }}>
          {hygieneBand ? (
            <button
              type="button"
              onClick={() => void compact()}
              disabled={compacting}
              style={{ height: "34px", padding: "0 14px", borderRadius: "9px", border: "1px solid rgba(180, 83, 9, 0.32)", background: "rgba(180, 83, 9, 0.1)", color: "#b45309", cursor: compacting ? "default" : "pointer", fontWeight: 650, opacity: compacting ? 0.6 : 1 }}
            >
              {compacting ? t("contextSaving.compacting") : t("contextSaving.compactNow")}
            </button>
          ) : null}
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
        title={chipTitle}
        aria-label={chipTitle}
        // 稳定契约：提示文字随语言变，端到端与无障碍都靠这两个属性认这个胶囊。
        data-testid="context-saving-chip"
        data-hygiene={hygiene?.state || "none"}
        style={{ display: "inline-flex", alignItems: "center", height: "20px", padding: "0 8px", borderRadius: "999px", border: hygieneBand ? "1px solid rgba(180, 83, 9, 0.32)" : "1px solid rgba(15, 118, 110, 0.24)", background: hygieneBand ? "linear-gradient(rgba(180, 83, 9, 0.1), rgba(180, 83, 9, 0.1)), var(--mobile-overlay-bg)" : "linear-gradient(rgba(15, 118, 110, 0.09), rgba(15, 118, 110, 0.09)), var(--mobile-overlay-bg)", color: hygieneBand ? "#b45309" : "#0f766e", fontSize: COMPOSER_CHIP_FONT_SIZE, fontWeight: 700, lineHeight: 1, whiteSpace: "nowrap", cursor: "pointer" }}
      >
        {chipLabel}
      </button>
      {modal}
    </>
  );
}
