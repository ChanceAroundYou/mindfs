import React, { useCallback, useEffect } from "react";
import { confirmDialog } from "../services/dialog";
import { useI18n } from "../i18n";

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

/** 面板右上角的 ×。三张面板共用，避免各画一个粗细不一样的。 */
export function CloseGlyph() {
  return (
    <svg
      width="12"
      height="12"
      viewBox="0 0 12 12"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      aria-hidden="true"
    >
      <line x1="2" y1="2" x2="10" y2="10" />
      <line x1="10" y1="2" x2="2" y2="10" />
    </svg>
  );
}

export type PanelShellProps = {
  title: React.ReactNode;
  /**
   * header 右侧（关闭 / 保存等）。传函数是为了把**受控的关闭**交回给 PanelShell ——
   * 否则调用方写 `onClick={onClose}` 会绕过放弃确认。返回 null 则不渲染。
   */
  headerRight?: (requestClose: () => void) => React.ReactNode;
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
  /**
   * 有未保存内容。返回 true 时，任何关闭入口（遮罩 / 右上角 × / Esc）都先问一句
   * 「是否放弃修改」再真关；返回 false（默认）直接关。
   *
   * 传函数而不是布尔：判断「有没有改东西」常常要看草稿与初值的对比（改名、改 prompt），
   * 而这些草稿住在各自的组件里，共享层拿不到。
   */
  hasUnsavedChanges?: () => boolean;
  /** 放弃确认的文案；不传用通用措辞。 */
  discardConfirmMessage?: string;
  /** 放弃确认按钮上的字；不传用通用措辞。 */
  discardConfirmLabel?: string;
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
  hasUnsavedChanges,
  discardConfirmMessage,
  discardConfirmLabel,
}: PanelShellProps) {
  const { t } = useI18n();
  // 关闭的三条路（遮罩 / Esc / 调用方按钮）都走这里，判定只写一遍。
  // 没有 onClose 时不算「有关闭动作」，直接放行。
  const requestClose = useCallback(() => {
    if (!onClose) return;
    if (!hasUnsavedChanges?.()) {
      onClose();
      return;
    }
    void confirmDialog({
      message: discardConfirmMessage || t("common.discardChangesConfirm"),
      confirmLabel: discardConfirmLabel || t("common.discardChanges"),
      cancelLabel: t("common.keepEditing"),
      danger: true,
    }).then((ok) => {
      if (ok) onClose();
    });
  }, [onClose, hasUnsavedChanges, discardConfirmMessage, discardConfirmLabel, t]);

  // Esc 关闭。捕获阶段监听：面板里是 Lexical/输入框，冒泡上来时可能被它们先处理掉。
  useEffect(() => {
    if (!onClose) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      requestClose();
    };
    document.addEventListener("keydown", onKeyDown, true);
    return () => document.removeEventListener("keydown", onKeyDown, true);
  }, [onClose, requestClose]);

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
        if (closeOnOverlayClick && event.target === event.currentTarget) requestClose();
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
          {headerRight?.(requestClose) ?? null}
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
