import React from "react";
import type { ReactNode } from "react";

/**
 * 会话底部的一行：左侧是**唯一**的动态槽位，右侧是常驻状态条。
 *
 * 左侧三态互斥、按优先级只显示一个：
 *   运行中（isAwaiting / isStreaming） > 子代理运行中（delegating） > 上一轮控件（复制/fork/时间）。
 * 具体选谁由调用方（SessionViewer）判定，本组件只保证「一个槽位、同一行、居左」。
 *
 * 右侧状态条不随运行状态消失，也不换行 —— 它描述会话的持续状态（模型/上下文/缓存），
 * 与轮次无关。
 */
export type SessionViewerBottomBarProps = {
  left: ReactNode;
  right: ReactNode;
};

export function SessionViewerBottomBar({ left, right }: SessionViewerBottomBarProps) {
  return (
    <div
      data-testid="session-bottom-bar"
      style={{
        display: "flex",
        flexWrap: "nowrap",
        alignItems: "center",
        gap: "12px",
        width: "100%",
        minWidth: 0,
        minHeight: "20px",
        marginTop: "16px",
        padding: "0 2px 4px",
        boxSizing: "border-box",
      }}
    >
      <div
        data-testid="session-bottom-left"
        style={{
          display: "flex",
          alignItems: "center",
          minWidth: 0,
          flex: "0 1 auto",
          overflow: "hidden",
        }}
      >
        {left}
      </div>
      <div style={{ flex: "1 1 0", minWidth: "8px" }} />
      <div
        data-testid="session-bottom-right"
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "flex-end",
          flexShrink: 0,
          minWidth: 0,
        }}
      >
        {right}
      </div>
    </div>
  );
}