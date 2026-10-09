import React from "react";

export function pencilStyle(disabled: boolean): React.CSSProperties {
  return {
    border: "none",
    background: "transparent",
    color: disabled ? "var(--text-secondary)" : "var(--accent-color)",
    borderRadius: 6,
    width: 24,
    height: 24,
    padding: 0,
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    cursor: disabled ? "not-allowed" : "pointer",
    flexShrink: 0,
    opacity: disabled ? 0.4 : 1,
  };
}

export const sessionIconButtonStyle: React.CSSProperties = {
  width: "22px",
  height: "22px",
  border: "none",
  borderRadius: "6px",
  background: "transparent",
  color: "var(--text-secondary)",
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  cursor: "pointer",
  padding: 0,
  marginLeft: "auto",
};

/* 阶段行右侧那组键里的「运行」键。贴右靠左边那组 flex:1，不靠 marginLeft:auto
   —— 一行里两个 auto 会把剩余空间对半分，中间裂一道缝（见阶段行那段注释）。 */
export const runIconButtonStyle: React.CSSProperties = {
  ...sessionIconButtonStyle,
  marginLeft: 0,
  color: "var(--accent-color)",
};

export const tagStyle: React.CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  gap: "4px",
  fontSize: "11px",
  fontWeight: 700,
  color: "var(--text-secondary)",
  background: "rgba(148, 163, 184, 0.12)",
  borderRadius: "999px",
  padding: "2px 8px",
};
