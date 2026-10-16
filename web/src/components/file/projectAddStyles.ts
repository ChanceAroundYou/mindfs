import type React from "react";

export const projectAddPopoverStyle: React.CSSProperties = {
  width: "248px",
  maxWidth: "calc(100vw - 32px)",
  padding: "10px",
  borderRadius: "12px",
  border: "1px solid var(--border-color)",
  background: "var(--menu-bg)",
  boxShadow: "0 12px 30px rgba(15, 23, 42, 0.14)",
  display: "flex",
  flexDirection: "column",
  gap: "10px",
};

/** 嵌进别人弹层时的形态：外框与动作按钮由调用方负责（见 LocalPanel 的 localBare）。 */
export const localBareStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "10px",
  minWidth: 0,
};
