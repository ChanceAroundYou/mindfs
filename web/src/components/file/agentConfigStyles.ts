import type React from "react";

export const agentConfigFieldStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "6px",
};

export const agentConfigLabelStyle: React.CSSProperties = {
  fontSize: "11px",
  fontWeight: 600,
  color: "var(--text-secondary)",
};

export const agentConfigInputStyle: React.CSSProperties = {
  width: "100%",
  borderRadius: "8px",
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-primary)",
  fontSize: "12px",
  padding: "8px 10px",
  outline: "none",
  boxSizing: "border-box",
};

export const agentConfigLineEditorStyle: React.CSSProperties = {
  ...agentConfigInputStyle,
  minHeight: "42px",
  maxHeight: "260px",
  overflow: "auto",
  padding: "6px",
  display: "flex",
  flexDirection: "column",
  gap: "4px",
};

export const agentConfigLineTextAreaStyle: React.CSSProperties = {
  width: "100%",
  border: "none",
  borderRadius: "6px",
  background: "rgba(148, 163, 184, 0.16)",
  color: "var(--text-primary)",
  fontSize: "12px",
  padding: "4px 8px",
  outline: "none",
  boxSizing: "border-box",
  minHeight: "28px",
  resize: "none",
  lineHeight: "20px",
  overflow: "hidden",
  fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
};

export const agentConfigHintStyle: React.CSSProperties = {
  borderRadius: "8px",
  padding: "8px 10px",
  fontSize: "12px",
  color: "var(--text-secondary)",
  background: "rgba(148, 163, 184, 0.10)",
  lineHeight: 1.45,
  wordBreak: "break-word",
};

export const agentConfigActionRowStyle: React.CSSProperties = {
  display: "grid",
  gridTemplateColumns: "1fr 1fr",
  gap: "8px",
};

export const agentConfigPrimaryButtonStyle = (disabled: boolean): React.CSSProperties => ({
  border: "none",
  background: "var(--accent-color)",
  color: "#fff",
  borderRadius: "8px",
  padding: "8px 10px",
  fontSize: "12px",
  fontWeight: 600,
  cursor: disabled ? "not-allowed" : "pointer",
  opacity: disabled ? 0.6 : 1,
});

export const agentConfigSecondaryButtonStyle = (disabled: boolean): React.CSSProperties => ({
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-secondary)",
  borderRadius: "8px",
  padding: "8px 10px",
  fontSize: "12px",
  fontWeight: 600,
  cursor: disabled ? "not-allowed" : "pointer",
  opacity: disabled ? 0.6 : 1,
});

export const agentConfigIconButtonStyle = (disabled: boolean): React.CSSProperties => ({
  width: "28px",
  height: "28px",
  border: "none",
  borderRadius: "8px",
  background: "transparent",
  color: "#dc2626",
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  cursor: disabled ? "not-allowed" : "pointer",
  opacity: disabled ? 0.5 : 0.9,
  flexShrink: 0,
});
