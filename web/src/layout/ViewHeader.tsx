import React, { createContext, useContext } from "react";
import { useI18n } from "../i18n";

/**
 * 移动端侧栏切换按钮的上下文。由 AppShell 提供（它已经算好了物理左右栏的开合、
 * 标签与 toggleRail），各视图的 ViewHeader 消费它，把两个按钮渲染进自己 header 的两侧。
 *
 * 为什么用 context 而不是逐层传 prop：header 在 6+ 个视图里，逐层透传要改每一条
 * 渲染链路（App → 各视图 → header），而它们的中间层与侧栏开合毫无关系。
 */
export type MobileSidebarToggleValue = {
  isMobile: boolean;
  leftOpen: boolean;
  rightOpen: boolean;
  leftLabel: string;
  rightLabel: string;
  toggle: (side: "left" | "right") => void;
};

export const MobileSidebarToggleContext = createContext<MobileSidebarToggleValue | null>(null);

// 左右侧栏切换按钮的图标：圆角矩形 + 一根竖分隔线。
// 线在左 = 左侧栏，线在右 = 右侧栏；按物理侧栏区分，不再按「文件/会话」内容区分。
const sidebarIcon = (side: "left" | "right") => (
  <svg
    xmlns="http://www.w3.org/2000/svg"
    width="18"
    height="18"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="2"
    aria-hidden="true"
  >
    <rect x="3" y="4" width="18" height="16" rx="2" />
    <line
      x1={side === "left" ? 9 : 15}
      y1="4"
      x2={side === "left" ? 9 : 15}
      y2="20"
    />
  </svg>
);

const toggleButtonStyle: React.CSSProperties = {
  width: "28px",
  height: "28px",
  minWidth: "28px",
  borderRadius: "5px",
  border: "none",
  background: "transparent",
  color: "var(--text-secondary)",
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  cursor: "pointer",
  opacity: 0.86,
  outline: "none",
  boxShadow: "none",
  padding: 0,
  WebkitTapHighlightColor: "transparent",
  flexShrink: 0,
};

type ViewHeaderProps = {
  /** 空 header（如 chat 空态）可以不传 children */
  children?: React.ReactNode;
  /** 桌面端内边距；移动端固定收成按钮两侧的小留白 */
  padding?: string;
  /** 覆盖/追加 header 自身样式（如 display:none、zIndex） */
  style?: React.CSSProperties;
  /** 移动端把 children 包进中间弹性容器时套用的内层样式（复刻原 header 的排布） */
  innerStyle?: React.CSSProperties;
  /**
   * 是否在移动端渲染左右侧栏切换按钮。默认 true（中间主栏的几个面板）。
   * 侧栏自身（FileTree / SessionList）传 false —— 侧栏已经可见，不需要切换按钮。
   */
  showToggles?: boolean;
  /** 透传到 header 元素（data-onboarding 等） */
  [key: `data-${string}`]: unknown;
};

/**
 * 各主视图共用的 36px header 外壳。桌面端行为与改造前逐字一致（children 直接渲染）；
 * 移动端在两侧插入侧栏切换按钮，children 收进中间的弹性容器。
 */
export function ViewHeader({ children, padding = "0 16px", style, innerStyle, showToggles = true, ...rest }: ViewHeaderProps) {
  const ctx = useContext(MobileSidebarToggleContext);
  const { t } = useI18n();
  const isMobile = ctx?.isMobile ?? false;
  const showButtons = isMobile && showToggles;

  const base: React.CSSProperties = {
    height: "36px",
    borderBottom: "1px solid var(--border-color)",
    display: "flex",
    alignItems: "center",
    background: "var(--mindfs-topbar-bg, transparent)",
    boxSizing: "border-box",
    flexShrink: 0,
  };

  const toggleButton = (side: "left" | "right") => {
    if (!ctx) return null;
    const open = side === "left" ? ctx.leftOpen : ctx.rightOpen;
    const label = side === "left" ? ctx.leftLabel : ctx.rightLabel;
    return (
      <button
        type="button"
        onClick={() => ctx.toggle(side)}
        aria-label={open ? t("sidebar.collapse", { label }) : t("sidebar.expand", { label })}
        title={open ? t("sidebar.collapse", { label }) : t("sidebar.expand", { label })}
        style={toggleButtonStyle}
      >
        {sidebarIcon(side)}
      </button>
    );
  };

  return (
    <header {...(rest as Record<string, unknown>)} style={{ ...base, padding: isMobile ? "0 2px" : padding, ...style }}>
      {showButtons ? toggleButton("left") : null}
      {isMobile && showToggles ? (
        <div style={{ flex: 1, minWidth: 0, display: "flex", alignItems: "center", ...innerStyle }}>{children}</div>
      ) : (
        children
      )}
      {showButtons ? toggleButton("right") : null}
    </header>
  );
}
