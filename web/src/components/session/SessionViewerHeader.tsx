import React from "react";
import { ViewHeader } from "../../layout/ViewHeader";
import { rootBadgeButtonStyle } from "../../shared/rootBadgeStyle";

export function SessionViewerHeader({ interactionMode, rootId, rootColor, rootDisplayName, displayName, onRootClick }: any) {
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
  </h1>
</ViewHeader>
  );
}
