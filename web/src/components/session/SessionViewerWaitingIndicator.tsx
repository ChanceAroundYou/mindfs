import React from "react";

export function SessionViewerWaitingIndicator({
  isAwaiting,
  isStreaming,
  streamStatusText,
  rootColor,
  t,
}: {
  isAwaiting: boolean;
  isStreaming: boolean;
  streamStatusText?: string;
  rootColor?: string | null;
  t: (key: any) => string;
}) {
  if (!isAwaiting && !isStreaming) return null;
  const awaitingColor = String(rootColor || "").trim() || "var(--accent-color)";
  return (
    <div style={{ marginTop: "16px", display: "flex", alignItems: "center", gap: "6px", fontSize: "12px", color: "var(--text-secondary)" }}>
      <span style={{ width: "8px", height: "8px", borderRadius: "50%", background: awaitingColor, animation: "pulse 1s infinite" }} />
      {isStreaming ? streamStatusText || t("session.generating") : t("session.sentWaiting")}
    </div>
  );
}
