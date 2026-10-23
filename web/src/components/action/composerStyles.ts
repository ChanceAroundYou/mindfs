import type React from "react";
import { appAccentHexToRgba } from "../../shared/actionBarUtils";

/** 对话输入框（ActionBar）与任务 PromptEditor 共用的样式。 */

/** 编辑区容器：外框 + 圆角 + 底色。已执行态传灰底。 */
export function composerContainerStyle(background: string): React.CSSProperties {
  return {
    position: "relative",
    border: "1px solid var(--border-color)",
    borderRadius: "8px",
    background,
    overflow: "auto",
    minHeight: "44px",
  };
}
/**
 * 编辑区字号刻度：单一来源。
 * token 编辑器（TokenEditor 内部）走 16px/20px，单行输入框走 13px，
 * 两者之前各写各的（12/12-bold/14 混着来），看着像三套控件。
 */
export const EDITOR_FONT_SIZE = 16;
export const EDITOR_LINE_HEIGHT = 20;
export const INPUT_FONT_SIZE = 13;

/**
 * 输入区徽标带（内存 / 限额 / Plan / worktree）的胶囊字号。
 * 比正文输入（13px）小一档，避免一排彩色胶囊比用户正在写的内容还抢眼；
 * 六处胶囊共用这一个值，改一档只动这里。
 */
export const COMPOSER_CHIP_FONT_SIZE = 9;

/** 单行紧凑输入框（模板名、阶段名、任务名等）。与 PromptEditor 同源边框/底色/字号。 */
export const composerInputStyle: React.CSSProperties = {
  boxSizing: "border-box",
  borderRadius: "6px",
  border: "1px solid var(--border-color)",
  background: "var(--input-bg)",
  color: "var(--text-color)",
  padding: "0 8px",
  fontSize: INPUT_FONT_SIZE,
  minWidth: 0,
  outline: "none",
};

/** 圆角图标按钮（+ 号、铅笔、附加操作）。 */
export function composerIconButtonStyle(opts: {
  enabled?: boolean;
  dimmed?: boolean;
  tone?: "default" | "accent" | "danger";
} = {}): React.CSSProperties {
  const { enabled = true, dimmed = false, tone = "default" } = opts;
  const color = dimmed
    ? "var(--text-secondary)"
    : tone === "accent"
      ? "var(--accent-color)"
      : tone === "danger"
        ? "#ef4444"
        : "var(--text-secondary)";
  return {
    width: "28px",
    height: "28px",
    borderRadius: "8px",
    border: "none",
    background: "transparent",
    color,
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    padding: 0,
    cursor: enabled && !dimmed ? "pointer" : "not-allowed",
    opacity: dimmed ? 0.35 : 1,
    transition: "all 0.2s",
    flexShrink: 0,
  };
}

/** hex 或 rgb 转 rgba（CSS 变量无法直接加透明度时用）。 */
export function hexToRgba(hex: string, alpha: number): string {
  const h = String(hex || "").trim().replace(/^#/, "");
  const fallback = `rgba(37, 99, 235, ${alpha})`;
  if (h.length === 3) {
    const r = parseInt(h[0] + h[0], 16);
    const g = parseInt(h[1] + h[1], 16);
    const b = parseInt(h[2] + h[2], 16);
    if (Number.isFinite(r) && Number.isFinite(g) && Number.isFinite(b)) return `rgba(${r}, ${g}, ${b}, ${alpha})`;
    return fallback;
  }
  if (h.length === 6) {
    const r = parseInt(h.slice(0, 2), 16);
    const g = parseInt(h.slice(2, 4), 16);
    const b = parseInt(h.slice(4, 6), 16);
    if (Number.isFinite(r) && Number.isFinite(g) && Number.isFinite(b)) return `rgba(${r}, ${g}, ${b}, ${alpha})`;
  }
  if (/^rgba?\(/.test(String(hex || ""))) return String(hex);
  return fallback;
}

/** 禁用按钮的灰底。原先引的 CSS 变量全仓库无定义（实际渲染成透明），看不出「不可按」。 */
const DISABLED_BUTTON_BG = "rgba(100, 116, 139, 0.22)";

/** 发送按钮：实心 accent 底，禁用时降透明度。取消态传 cancel。 */
export function composerSendButtonStyle(opts: {
  enabled?: boolean;
  cancel?: boolean;
  cancelling?: boolean;
  busy?: boolean;
  /** "composer"（对话输入框，无边框，默认）/ "panel"（任务面板，带外框） */
  variant?: "composer" | "panel";
  /** 节点主题色（hex），variant="panel" 时用于边框与底色 */
  accent?: string;
} = {}): React.CSSProperties {
  const { enabled = true, cancel = false, cancelling = false, variant = "composer", accent } = opts;
  const panel = variant === "panel";
  const accentColor = String(accent || "").trim();

  if (panel) {
    if (cancel) {
      return {
        width: "28px",
        height: "28px",
        borderRadius: "8px",
        border: "1px solid rgba(239,68,68,0.45)",
        background: "rgba(239,68,68,0.12)",
        color: "#ef4444",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: 0,
        cursor: cancelling ? "wait" : "pointer",
        transition: "all 0.2s",
        flexShrink: 0,
      };
    }
    return {
      width: "28px",
      height: "28px",
      borderRadius: "8px",
      border: enabled && accentColor
        ? `1px solid ${accentColor}`
        : enabled
          ? "1px solid var(--accent-color)"
          : "1px solid var(--border-color)",
      background: enabled
        ? (accentColor || "var(--accent-color)")
        : DISABLED_BUTTON_BG,
      color: enabled ? "#fff" : "var(--text-secondary)",
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
      padding: 0,
      cursor: enabled ? "pointer" : "not-allowed",
      transition: "all 0.2s",
      opacity: enabled ? 1 : 0.75,
      flexShrink: 0,
    };
  }

  return {
    width: "28px",
    height: "28px",
    borderRadius: "8px",
    border: "none",
    background: cancel ? "rgba(239,68,68,0.14)" : enabled ? "var(--accent-color)" : "transparent",
    color: cancel ? "#ef4444" : enabled ? "#fff" : "var(--text-secondary)",
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    padding: 0,
    cursor: cancel ? (cancelling ? "wait" : "pointer") : enabled ? "pointer" : "not-allowed",
    transition: "all 0.2s",
    opacity: cancel ? 1 : enabled ? 1 : 0.3,
    flexShrink: 0,
  };
}
