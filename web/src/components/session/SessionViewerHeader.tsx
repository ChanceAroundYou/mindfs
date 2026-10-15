import React from "react";
import { ViewHeader } from "../../layout/ViewHeader";
import { rootBadgeButtonStyle } from "../../shared/rootBadgeStyle";
import { useI18n } from "../../i18n";

export function SessionViewerHeader({ interactionMode, rootId, rootColor, rootDisplayName, displayName, isDelegating, onRootClick }: any) {
  const { t } = useI18n();
  const accent = String(rootColor || "").trim() || "var(--accent-color)";
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
    {/* 委托中：与列表点、底部文案同一形态（空心、不闪），切会话不抖。 */}
    {isDelegating ? (
      <span
        aria-label={t("session.subagentRunning")}
        title={t("session.subagentRunning")}
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
