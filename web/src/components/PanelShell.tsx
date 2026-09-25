import React from "react";

/**
 * 弹窗外壳：遮罩 + 圆角卡 + header。
 *
 * 三张任务面板（任务模板编辑 / 任务详情 / 新建任务）以前各手搓一遍同样的
 * 遮罩、圆角、阴影，zIndex 还各不同。这里收敛成一处。
 */

export function panelButtonStyle(kind: "primary" | "secondary" | "danger"): React.CSSProperties {
  const border = kind === "primary"
    ? "1px solid var(--accent-color)"
    : kind === "danger"
      ? "1px solid rgba(220, 38, 38, 0.45)"
      : "1px solid var(--border-color)";
  const background = kind === "primary"
    ? "var(--accent-color)"
    : kind === "danger"
      ? "rgba(220, 38, 38, 0.10)"
      : "var(--button-bg)";
  const color = kind === "primary"
    ? "#fff"
    : kind === "danger"
      ? "#dc2626"
      : "var(--text-color)";
  return {
    height: "30px",
    borderRadius: "6px",
    border,
    background,
    color,
    padding: "0 12px",
    fontSize: "12px",
    fontWeight: 700,
    cursor: "pointer",
    whiteSpace: "nowrap",
  };
}

/** 卡片里的圆形图标按钮（删除等） */
export function panelIconButtonStyle(danger = false, disabled = false): React.CSSProperties {
  return {
    width: "30px",
    height: "30px",
    borderRadius: "8px",
    border: "none",
    background: "transparent",
    color: danger && !disabled ? "#dc2626" : "var(--text-secondary)",
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    cursor: disabled ? "not-allowed" : "pointer",
    opacity: disabled ? 0.4 : 1,
    flexShrink: 0,
  };
}

export type PanelShellProps = {
  title: React.ReactNode;
  /** header 右侧（关闭 / 保存等） */
  headerRight?: React.ReactNode;
  /** header 标题左侧的额外内容（状态徽章等） */
  headerLeft?: React.ReactNode;
  children: React.ReactNode;
  footer?: React.ReactNode;
  /** 卡片最大宽度，默认 720 */
  width?: number;
  /** 卡片最小高度，默认不设 */
  minHeight?: number;
  onClose?: () => void;
  /** 点遮罩关闭（任务详情有，模板弹窗没有） */
  closeOnOverlayClick?: boolean;
  /** 遮罩下方额外内容（错误条等） */
  belowHeader?: React.ReactNode;
};

export function PanelShell({
  title,
  headerRight,
  headerLeft,
  children,
  footer,
  width = 720,
  minHeight,
  onClose,
  closeOnOverlayClick,
  belowHeader,
}: PanelShellProps) {
  return (
    <div
      className="mindfs-panel-overlay"
      style={{
        position: "fixed",
        inset: 0,
        zIndex: 90,
        background: "rgba(15, 23, 42, 0.36)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: "24px",
      }}
      onClick={(event) => {
        if (closeOnOverlayClick && onClose && event.target === event.currentTarget) onClose();
      }}
    >
      <section
        className="mindfs-panel-dialog"
        style={{
          width: `min(${width}px, 100%)`,
          maxHeight: "88dvh",
          ...(minHeight ? { minHeight } : {}),
          overflow: "hidden",
          borderRadius: "10px",
          background: "var(--menu-bg)",
          border: "1px solid var(--border-color)",
          boxShadow: "0 24px 60px rgba(15, 23, 42, 0.24)",
          display: "flex",
          flexDirection: "column",
        }}
      >
        <style>{`
          @media (max-width: 640px) {
            .mindfs-panel-overlay {
              top: 36px !important;
              align-items: flex-start !important;
              padding: 8px 12px 12px !important;
            }
            .mindfs-panel-dialog {
              width: 100% !important;
              height: auto !important;
              max-height: 60dvh !important;
            }
            .mindfs-panel-body {
              min-height: 0 !important;
              overflow: auto !important;
              -webkit-overflow-scrolling: touch;
            }
          }
        `}</style>
        <header
          style={{
            padding: "10px 14px",
            borderBottom: "1px solid var(--border-color)",
            display: "flex",
            alignItems: "center",
            gap: "8px",
            flexShrink: 0,
          }}
        >
          {headerLeft}
          <div style={{ flex: 1, minWidth: 0, display: "flex", alignItems: "center", gap: "8px", flexWrap: "wrap" }}>
            {title}
          </div>
          {headerRight}
        </header>
        {belowHeader}
        <div className="mindfs-panel-body" style={{ flex: 1, minHeight: 0, overflow: "auto", display: "flex", flexDirection: "column" }}>
          {children}
        </div>
        {footer ? (
          <div
            style={{
              padding: "10px 14px",
              borderTop: "1px solid var(--border-color)",
              display: "flex",
              alignItems: "center",
              justifyContent: "flex-end",
              gap: "8px",
              flexShrink: 0,
            }}
          >
            {footer}
          </div>
        ) : null}
      </section>
    </div>
  );
}
