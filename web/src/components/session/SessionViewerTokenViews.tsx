import React from "react";
import type { TokenUsage } from "../../shared/sessionTypes";
import { formatCompactTokenCount, formatContextWindowPercent } from "./sessionViewerFormat";

/**
 * 缓存命中率徽标（常驻状态条用）。
 *
 * 只显示 harness **确实上报** 的缓存读字段；没有 `cacheReadTokens` 时返回 null，
 * 由状态条隐藏该项 —— 不能把「未上报」画成 0%。
 */
export function CacheHitBadge({ usage }: { usage?: TokenUsage }) {
  if (!usage || !Number.isFinite(usage.cacheReadTokens)) {
    return null;
  }
  const inputTokens = Math.max(0, Number(usage.inputTokens || 0));
  if (inputTokens <= 0) {
    return null;
  }
  const cacheReadTokens = Math.max(0, Number(usage.cacheReadTokens || 0));
  const percent = Math.round(Math.min(1, cacheReadTokens / inputTokens) * 100);
  return (
    <span
      data-testid="status-cache"
      title={`♻ ${formatCompactTokenCount(cacheReadTokens)}/${formatCompactTokenCount(inputTokens)} cached`}
      style={{ fontVariantNumeric: "tabular-nums" }}
    >
      {`♻ ${percent}%`}
    </span>
  );
}

export function ContextWindowBadge({
  contextWindow,
}: {
  contextWindow?: { totalTokens: number; modelContextWindow: number };
}) {
  const metrics = formatContextWindowPercent(contextWindow);
  if (!metrics) {
    return null;
  }
  const hue =
    metrics.percent >= 90
      ? "#dc2626"
      : metrics.percent >= 75
        ? "#ea580c"
        : "#0f766e";
  return (
    <span
      title={`Context Window ${metrics.percent}% used (${metrics.usedTokens}/${contextWindow?.modelContextWindow} used)`}
      style={{
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        flexWrap: "wrap",
        minWidth: 0,
        color: hue,
        lineHeight: 1.1,
        fontSize: "10px",
        fontWeight: 700,
        letterSpacing: "0.01em",
        fontVariantNumeric: "tabular-nums",
        overflowWrap: "anywhere",
      }}
    >
      <span>{metrics.percent}%</span>
      <span>&middot;used</span>
      <span>
        {`(${formatCompactTokenCount(metrics.usedTokens)}/${formatCompactTokenCount(
          contextWindow?.modelContextWindow || 0,
        )})`}
      </span>
    </span>
  );
}
