import React from "react";

// 会话与任务详情共用的「回到底部」按钮。
//
// 外观取自会话浮层（G-KB 之后一直沿用的那一颗）：34px 圆钮、主题色底、白色下箭头。
// 两处必须长得一样，用户才认得出是同一个动作；逻辑（何时显示、怎么收敛）在
// scrollToBottom.ts 与各自的滚动 hook 里，这里只负责外观。

export type ScrollToBottomButtonProps = {
  /** 无障碍名称与 tooltip。 */
  label: string;
  onClick: () => void;
  themeColor?: string;
  testId?: string;
};

export function ScrollToBottomButton({
  label,
  onClick,
  themeColor = "var(--accent-color)",
  testId,
}: ScrollToBottomButtonProps) {
  return (
    <button
      type="button"
      data-testid={testId}
      onClick={onClick}
      aria-label={label}
      title={label}
      style={{
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        width: "34px",
        height: "34px",
        border: `1px solid color-mix(in srgb, ${themeColor} 35%, transparent)`,
        background: themeColor,
        color: "#ffffff",
        borderRadius: "999px",
        boxShadow: "0 8px 24px rgba(15, 23, 42, 0.14)",
        cursor: "pointer",
        fontSize: "12px",
        whiteSpace: "nowrap",
      }}
    >
      <svg
        xmlns="http://www.w3.org/2000/svg"
        width="16"
        height="16"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <path d="M12 5v14" />
        <path d="m19 12-7 7-7-7" />
      </svg>
    </button>
  );
}
