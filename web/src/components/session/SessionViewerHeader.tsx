import React from "react";
import { ViewHeader } from "../../layout/ViewHeader";
import { rootBadgeButtonStyle } from "../../shared/rootBadgeStyle";
import { useI18n } from "../../i18n";

export function SessionViewerHeader({ interactionMode, rootId, rootColor, rootDisplayName, displayName, isDelegating, isAwaitingAnswer, onRootClick }: any) {
  const { t } = useI18n();
  const accent = String(rootColor || "").trim() || "var(--accent-color)";
  // 两档空心共用同一个灯形态，只有文案不同（等回答 = 轮到你说话；委托 = 等子代理）。
  const hollowLabel = isAwaitingAnswer
    ? t("session.awaitingAnswer")
    : isDelegating
      ? t("session.subagentRunning")
      : "";
  return (
<ViewHeader padding="0 16px" style={{ zIndex: 10 }}>
  <h1
    style={{
      display: "flex",
      alignItems: "center",
      gap: "8px",
      fontSize: "14px",
      fontWeight: 600,
      margin: 0,
      minWidth: 0,
    }}
  >
    {rootId ? (
      <button
        type="button"
        onClick={() => onRootClick?.(rootId)}
        style={{
          ...rootBadgeButtonStyle,
          background: "var(--node-badge-bg)",
          color: String(rootColor || "").trim() || "var(--text-primary)",
          flexShrink: 0,
          cursor: onRootClick ? "pointer" : "default",
        }}
      >
        {rootDisplayName || rootId}
      </button>
    ) : null}
    <span
      style={{
        minWidth: 0,
        overflow: "hidden",
        textOverflow: "ellipsis",
        whiteSpace: "nowrap",
      }}
    >
      {displayName}
    </span>
    {/* 委托中/等回答：与列表点、底部文案同一形态（空心、不闪），切会话不抖。 */}
    {hollowLabel ? (
      <span
        aria-label={hollowLabel}
        title={hollowLabel}
        style={{
          width: "8px",
          height: "8px",
          borderRadius: "999px",
          flexShrink: 0,
          boxSizing: "border-box",
          border: `1.5px solid ${accent}`,
          background: "transparent",
        }}
      />
    ) : null}
  </h1>
</ViewHeader>
  );
}
