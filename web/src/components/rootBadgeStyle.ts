import type { CSSProperties } from "react";

// 消费者（FileViewer/DefaultListView/SessionViewer/GitDiffViewer）都会用各自拿到的
// 节点色覆盖 color；这里只在节点色缺失时兜底，不再有独立于节点色的主题色。
export const rootBadgeStyle: CSSProperties = {
  display: "inline-block",
  fontSize: "13px",
  lineHeight: "1.2",
  fontWeight: 600,
  color: "var(--text-primary)",
  background: "var(--node-badge-bg)",
  borderRadius: "6px",
  padding: "1px 4px",
  boxSizing: "border-box",
  verticalAlign: "top",
};

export const rootBadgeButtonStyle: CSSProperties = {
  ...rootBadgeStyle,
  border: "none",
};
