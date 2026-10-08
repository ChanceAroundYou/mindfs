import type React from "react";

export function buildUserMetaButtonStyle(themeColor: string): React.CSSProperties {
  return {
    width: "18px", height: "18px", border: "none", background: "transparent",
    padding: 0, margin: 0, color: themeColor, cursor: "pointer", fontSize: "14px",
    fontWeight: 800, lineHeight: 1, opacity: 1, display: "inline-flex",
    alignItems: "center", justifyContent: "center", borderRadius: "6px", flexShrink: 0,
  };
}
