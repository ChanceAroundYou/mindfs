import React, { useEffect, useRef, useState } from "react";
import { usePanelShellClose } from "../../hooks/usePanelShellClose";
import { useSelectionContainment } from "../../hooks/useSelectionContainment";
import { useI18n } from "../../i18n/index";

/**
 * 弹窗外壳：遮罩 + 圆角卡 + header。
 *
 * 任务面板（任务详情 / 新建任务）以前各手搓一遍同样的
 * 遮罩、圆角、阴影，zIndex 还各不同。这里收敛成一处。
 *
 * 默认关闭只有「点空白（遮罩）」一条路：面板里不放取消键、也不放右上角 ×。
 * 需要显式关闭控件的面板传 showCloseButton，右上角多出一颗 ×；它和遮罩走同一个
 * requestClose，改到一半点出去（或点 ×）都会先问一句「是否放弃修改」，见
 * hasUnsavedChanges。
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

/**
 * 面板里的输入控件（input / textarea）。
 *
 * 工作流编辑器的输入框原先是手写的 border/padding，跟任务面板的输入框差一档
 * 字号与圆角，摆在同一屏里像两套 UI。
 */
export function panelInputStyle(): React.CSSProperties {
  return {
    minHeight: "30px",
    borderRadius: "6px",
    border: "1px solid var(--border-color)",
    background: "var(--button-bg)",
    color: "var(--text-color)",
    padding: "4px 8px",
    fontSize: "12px",
    fontFamily: "inherit",
  };
}

/** 面板里的卡片容器（分组区块、画布视口、只读清单条目）。 */
export function panelSurfaceStyle(): React.CSSProperties {
  return {
    borderRadius: "8px",
    border: "1px solid var(--border-color)",
    background: "var(--menu-bg)",
    padding: "10px 12px",
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
  /** header 右侧（保存等）。默认关闭只走遮罩；要 × 请开 showCloseButton。 */
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
  /**
   * header 右上角是否显示关闭（×）按钮，默认不显示。
   *
   * 打开它不会绕开关闭守卫：按钮复用 requestClose，有未保存内容时同样先确认；
   * 遮罩那条路也照旧可用。
   */
  showCloseButton?: boolean;
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
  showCloseButton = false,
  belowHeader,
  hasUnsavedChanges,
}: PanelShellProps) {
  const { t } = useI18n();
  // 关闭守卫和放弃确认由逻辑 hook 统一处理。
  const requestClose = usePanelShellClose({ onClose, hasUnsavedChanges, t });
  // CUSTOM(G-MA)：面板正文（任务对话等）拖选复制时，选区不许扩到面板标题栏。
  const bodyRef = useRef<HTMLDivElement>(null);
  useSelectionContainment(bodyRef);

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
              /* 任务 #87 第 5 条：遮罩从 top:36px 开始，卡片却按整屏高度封顶，
                 结果卡片比遮罩高 36px+padding —— 底部（运行视图）露在可见区外，
                 正文滚到底也看不见。把顶栏 36px 与上下 padding 20px 一并扣掉。 */
              max-height: calc(var(--panel-vh, 100dvh) - 56px) !important;
            }
            .mindfs-panel-body {
              min-height: 0 !important;
              overflow: auto !important;
              -webkit-overflow-scrolling: touch;
              /* 滚到边界别把滚动传给背后的页面（否则一滑就整个页面跟着动）。 */
              overscroll-behavior: contain;
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
            // CUSTOM(G-MA)：标题栏外壳**不能**设 user-select:none —— 整栏不可选时，
            // 从正文向上拖选 + 浏览器自动滚动会让 Chromium 静默清空整个选区。
            // 越界的选区由 useSelectionContainment 在抬手时拉回正文。
          }}
        >
          {headerLeft}
          {/* 标题槽是内容（任务名可改可复制），保持可选。 */}
          <div style={{ flex: 1, minWidth: 0, display: "flex", alignItems: "center", gap: "8px", flexWrap: "wrap", userSelect: "text" }}>
            {title}
          </div>
          {headerRight}
          {showCloseButton ? (
            // 显式关闭入口：和遮罩同一条 requestClose，未保存修改照样先确认。
            <button
              type="button"
              aria-label={t("common.close")}
              title={t("common.close")}
              onClick={() => requestClose()}
              style={{
                width: "28px",
                height: "28px",
                flexShrink: 0,
                padding: 0,
                border: "none",
                borderRadius: "8px",
                background: "transparent",
                color: "var(--text-secondary)",
                display: "inline-flex",
                alignItems: "center",
                justifyContent: "center",
                cursor: "pointer",
              }}
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
                <path d="M6 6l12 12M18 6L6 18" />
              </svg>
            </button>
          ) : null}
        </header>
        {belowHeader}
        <div ref={bodyRef} className="mindfs-panel-body" style={{ flex: 1, minHeight: 0, overflow: "auto", display: "flex", flexDirection: "column", userSelect: "text" }}>
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
