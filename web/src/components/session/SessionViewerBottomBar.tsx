import React from "react";
import type { ReactNode } from "react";

/**
 * 消息流里的最后一行：左侧是尾部动态内容，右侧是状态条。
 *
 * 它不是固定悬浮条 —— 和消息一起滚动，上滚就离开视口。左侧三态互斥、按优先级只显示一个：
 *   运行中（isAwaiting / isStreaming） > 子代理运行中（delegating） > 最新完成轮控件。
 * 历史轮次的控件行仍在各自的时间线收尾位置，只有这一行带右侧状态。
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