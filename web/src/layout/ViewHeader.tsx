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
  leftIsSession: boolean;
  rightIsSession: boolean;
  toggle: (side: "left" | "right") => void;
};

export const MobileSidebarToggleContext = createContext<MobileSidebarToggleValue | null>(null);

const fileSidebarIcon = (
  <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true">
    <path fill="currentColor" d="M3 3h6v4H3zm12 7h6v4h-6zm0 7h6v4h-6zm-2-4H7v5h6v2H5V9h2v2h6z" style={{ transform: "scale(1.28)", transformOrigin: "12px 12px" }} />
  </svg>
);

const sessionSidebarIcon = (
  <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3.8" strokeLinecap="round" aria-hidden="true">
    <line x1="6" y1="4" x2="18" y2="4" />
    <line x1="6" y1="12" x2="18" y2="12" />
    <line x1="6" y1="20" x2="18" y2="20" />
  </svg>
);

const toggleButtonStyle: React.CSSProperties = {
  width: "28px",
  height: "28px",
  minWidth: "28px",
  borderRadius: "6px",
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
  children: React.ReactNode;
  /** 桌面端内边距；移动端固定收成按钮两侧的小留白 */
  padding?: string;
  /** 覆盖/追加 header 自身样式（如 display:none、zIndex） */
  style?: React.CSSProperties;
  /** 移动端把 children 包进中间弹性容器时套用的内层样式（复刻原 header 的排布） */
  innerStyle?: React.CSSProperties;
  /** 透传到 header 元素（data-onboarding 等） */
  [key: `data-${string}`]: unknown;
};

/**
 * 各主视图共用的 36px header 外壳。桌面端行为与改造前逐字一致（children 直接渲染）；
 * 移动端在两侧插入侧栏切换按钮，children 收进中间的弹性容器。
 */
export function ViewHeader({ children, padding = "0 16px", style, innerStyle, ...rest }: ViewHeaderProps) {
  const ctx = useContext(MobileSidebarToggleContext);
  const { t } = useI18n();
  const isMobile = ctx?.isMobile ?? false;

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
    const isSession = side === "left" ? ctx.leftIsSession : ctx.rightIsSession;
    return (
      <button
        type="button"
        onClick={() => ctx.toggle(side)}
        aria-label={open ? t("sidebar.collapse", { label }) : t("sidebar.expand", { label })}
        title={open ? t("sidebar.collapse", { label }) : t("sidebar.expand", { label })}
        style={toggleButtonStyle}
      >
        {isSession ? sessionSidebarIcon : fileSidebarIcon}
      </button>
    );
  };

  return (
    <header {...(rest as Record<string, unknown>)} style={{ ...base, padding: isMobile ? "0 2px" : padding, ...style }}>
      {isMobile ? toggleButton("left") : null}
      {isMobile ? (
        <div style={{ flex: 1, minWidth: 0, display: "flex", alignItems: "center", ...innerStyle }}>{children}</div>
      ) : (
        children
      )}
      {isMobile ? toggleButton("right") : null}
    </header>
  );
}
