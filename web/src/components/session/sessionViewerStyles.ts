import type React from "react";

export function buildUserMetaButtonStyle(themeColor: string): React.CSSProperties {
  return {
    width: "18px", height: "18px", border: "none", background: "transparent",
    padding: 0, margin: 0, color: themeColor, cursor: "pointer", fontSize: "14px",
    fontWeight: 800, lineHeight: 1, opacity: 1, display: "inline-flex",
    alignItems: "center", justifyContent: "center", borderRadius: "6px", flexShrink: 0,
  };
}

export const sessionViewerAnimationStyles = `
  @keyframes pulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.5; }
  }
  @keyframes sessionLoadingPulse {
    0% { background-position: 100% 0; opacity: 0.7; }
    50% { opacity: 1; }
    100% { background-position: -100% 0; opacity: 0.7; }
  }
  @keyframes spin {
    to { transform: rotate(360deg); }
  }
`;
