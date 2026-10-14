import React from "react";
import type { TokenUsage } from "../../shared/sessionTypes";
import { formatCompactTokenCount, formatContextWindowPercent, formatTurnTokenCount } from "./sessionViewerFormat";

export function TurnTokenUsage({ usage }: { usage?: TokenUsage }) {
  if (!usage) {
    return null;
  }
  const inputTokens = Math.max(0, Number(usage.inputTokens || 0));
  const outputTokens = Math.max(0, Number(usage.outputTokens || 0));
  if (!inputTokens && !outputTokens) {
    return null;
  }
  const cacheReadReported = Number.isFinite(usage.cacheReadTokens);
  const cacheReadTokens = Math.max(0, Number(usage.cacheReadTokens || 0));
  const hitPercent = inputTokens > 0 && cacheReadReported
    ? Math.round(Math.min(1, cacheReadTokens / inputTokens) * 100)
    : null;
  const cacheLabel = hitPercent === null ? "—" : `${hitPercent}%`;
  return (
    <span
      title={`${inputTokens}(♻ ${cacheLabel})→${outputTokens}`}
      style={{
        display: "inline-flex",
        alignItems: "baseline",
        flexWrap: "wrap",
        minWidth: 0,
        fontVariantNumeric: "tabular-nums",
      }}
    >
      <span>{formatTurnTokenCount(inputTokens)}</span>
      <span>{`(♻ ${cacheLabel})`}</span>
      <span>→</span>
      <span>{formatTurnTokenCount(outputTokens)}</span>
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
