import React from "react";

export function SessionViewerWaitingIndicator({
  isAwaiting,
  isStreaming,
  isDelegating,
  isAwaitingAnswer,
  streamStatusText,
  rootColor,
  t,
}: {
  isAwaiting: boolean;
  isStreaming: boolean;
  isDelegating?: boolean;
  isAwaitingAnswer?: boolean;
  streamStatusText?: string;
  rootColor?: string | null;
  t: (key: any) => string;
}) {
  // 空心两档：等回答（轮到你说话）、委托中（等子代理）。优先级都低于自跑 ——
  // 自己在跑时仍走「正在生成/已发送」。
  const delegating = !isAwaiting && !isStreaming && !!isDelegating;
  const awaitingAnswer = !isAwaiting && !isStreaming && !!isAwaitingAnswer;
  const hollow = delegating || awaitingAnswer;
  if (!isAwaiting && !isStreaming && !hollow) return null;
  const awaitingColor = String(rootColor || "").trim() || "var(--accent-color)";
  return (
    <div style={{ marginTop: "16px", display: "flex", alignItems: "center", gap: "6px", fontSize: "12px", color: "var(--text-secondary)" }}>
      <span
        style={
          hollow
            ? { width: "8px", height: "8px", borderRadius: "50%", background: "transparent", border: `1.5px solid ${awaitingColor}`, boxSizing: "border-box" }
            : { width: "8px", height: "8px", borderRadius: "50%", background: awaitingColor, animation: "pulse 1s infinite" }
        }
      />
      {awaitingAnswer
        ? t("session.awaitingAnswer")
        : delegating
          ? t("session.subagentRunning")
          : isStreaming
            ? streamStatusText || t("session.generating")
            : t("session.sentWaiting")}
    </div>
  );
}
