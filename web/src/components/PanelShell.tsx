import React, { useCallback, useEffect, useState } from "react";
import { confirmDialog } from "../services/dialog";
import { useI18n } from "../i18n";

/**
 * 弹窗外壳：遮罩 + 圆角卡 + header。
 *
 * 三张任务面板（任务模板编辑 / 任务详情 / 新建任务）以前各手搓一遍同样的
 * 遮罩、圆角、阴影，zIndex 还各不同。这里收敛成一处。
 *
 * 关闭只有「点空白（遮罩）」一条路：面板里不放取消键、也不放右上角 ×。
 * 改到一半点出去会先问一句「是否放弃修改」，见 hasUnsavedChanges。
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
  /** header 右侧（保存等）。关闭一律走遮罩，这里不摆关闭控件。 */
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
  belowHeader,
  hasUnsavedChanges,
}: PanelShellProps) {
  const { t } = useI18n();
  // 关闭只有一条路：点遮罩。判定只写一遍，所以将来要加确认/拦截也只需改这里。
  const requestClose = useCallback(() => {
    if (!onClose) return;
    if (!hasUnsavedChanges?.()) {
      onClose();
      return;
    }
    void confirmDialog({
      message: t("common.discardChangesConfirm"),
      confirmLabel: t("common.discardChanges"),
      cancelLabel: t("common.keepEditing"),
      danger: true,
    }).then((ok) => {
      if (ok) onClose();
    });
  }, [onClose, hasUnsavedChanges, t]);

  // 跟着可视视口把可用高度写成 --panel-vh，面板据此上浮避让软键盘。
  // 必须用 visualViewport：键盘弹出时 layout 视口（100vh / dvh）纹丝不动，
  // 只有 visualViewport 变矮，所以内联高度得从它取。
  const [viewportHeight, setViewportHeight] = useState(0);
  useEffect(() => {
    const vv = window.visualViewport;
    if (!vv) return;
    const sync = () => setViewportHeight(vv.height);
    sync();
    vv.addEventListener("resize", sync);
    vv.addEventListener("scroll", sync);
    return () => {
      vv.removeEventListener("resize", sync);
      vv.removeEventListener("scroll", sync);
    };
  }, []);

  return (
    <div
      className="mindfs-panel-overlay"
      style={{
        position: "fixed",
        // inset:0 会把遮罩钉在 layout 视口，键盘弹出后它和卡片一起被压在键盘下面。
        // 改用可视视口的 top/height，卡片才能真正停在键盘上方那块可见区域里。
        top: 0,
        left: 0,
        right: 0,
        height: viewportHeight ? `${viewportHeight}px` : "100dvh",
        zIndex: 90,
        background: "rgba(15, 23, 42, 0.36)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: "24px",
        // 供上面的 max-height 表达式取值；0 高度时 CSS 回退到 100dvh。
        ...(viewportHeight ? { "--panel-vh": `${viewportHeight}px` } : null),
      } as React.CSSProperties}
      onClick={(event) => {
        if (event.target === event.currentTarget) requestClose();
      }}
    >
      <section
        className="mindfs-panel-dialog"
        style={{
          width: `min(${width}px, 100%)`,
          // 高度上限跟着**可视视口**走。移动端键盘弹出时 layout 视口不动、
          // visualViewport 变矮，写死 dvh/vh 的话面板下半截会躲到键盘后面 ——
          // 也就是「输入框在屏幕外、看不见自己打了什么」。
          maxHeight: "min(88dvh, calc(var(--panel-vh, 100dvh) - 48px))",
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
              /* 顶栏（36px）之下居中，留出 8px 呼吸位。alignItems 换 flex-start，
                 具体上浮多少交给内联的 --panel-vh（见上），CSS 里写死 60dvh
                 只在没有该变量时兜底。 */
              top: 36px !important;
              align-items: flex-start !important;
              padding: 8px 12px 12px !important;
            }
            .mindfs-panel-dialog {
              width: 100% !important;
              height: auto !important;
              max-height: var(--panel-vh, 60dvh) !important;
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
