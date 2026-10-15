import React from "react";

export function SessionViewerWaitingIndicator({
  isAwaiting,
  isStreaming,
  isDelegating,
  streamStatusText,
  rootColor,
  t,
}: {
  isAwaiting: boolean;
  isStreaming: boolean;
  isDelegating?: boolean;
  streamStatusText?: string;
  rootColor?: string | null;
  t: (key: any) => string;
}) {
  // 委托态：自己闲、后代在跑。优先级低于自跑 —— 自己在跑时仍走「正在生成/已发送」。
  const delegating = !isAwaiting && !isStreaming && !!isDelegating;
  if (!isAwaiting && !isStreaming && !delegating) return null;
  const awaitingColor = String(rootColor || "").trim() || "var(--accent-color)";
  return (
    <div style={{ marginTop: "16px", display: "flex", alignItems: "center", gap: "6px", fontSize: "12px", color: "var(--text-secondary)" }}>
      <span
        style={
          delegating
            ? { width: "8px", height: "8px", borderRadius: "50%", background: "transparent", border: `1.5px solid ${awaitingColor}`, boxSizing: "border-box" }
            : { width: "8px", height: "8px", borderRadius: "50%", background: awaitingColor, animation: "pulse 1s infinite" }
        }
      />
      {delegating
        ? t("session.subagentRunning")
        : isStreaming
          ? streamStatusText || t("session.generating")
          : t("session.sentWaiting")}
    </div>
  );
}
