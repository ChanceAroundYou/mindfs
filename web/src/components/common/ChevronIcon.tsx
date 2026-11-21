import React from "react";

/**
 * 展开/收起记号：同一枚人字箭头，展开时旋转 90°。
 *
 * 任务详情的两个胶囊原来用 `▸ / ▾` 字符，字号和基线都跟着字体走，看起来像正文标点
 * 而不是控件。用 SVG 后两种状态由同一个形状旋转表达，点击时有过场，读屏仍靠
 * 按钮自己的 aria-expanded（这里只画图形，不承载语义）。
 */
export function ChevronIcon({ open, size = 12 }: { open: boolean; size?: number }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      style={{
        flexShrink: 0,
        transform: open ? "rotate(90deg)" : "rotate(0deg)",
        transition: "transform 0.15s ease",
      }}
    >
      <path d="m9 6 6 6-6 6" />
    </svg>
  );
}
