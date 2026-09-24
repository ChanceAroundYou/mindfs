import React from "react";

/**
 * 对话输入框（ActionBar）与任务 PromptEditor 共用的样式与图标。
 * 单一来源：改这里，两处一起变。
 */

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
        : "var(--button-bg)",
      color: enabled ? "#fff" : "var(--text-secondary)",
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
      padding: 0,
      cursor: enabled ? "pointer" : "not-allowed",
      transition: "all 0.2s",
      opacity: enabled ? 1 : 0.65,
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

export function PlusIcon({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" aria-hidden="true">
      <path d="M12 5v14" />
      <path d="M5 12h14" />
    </svg>
  );
}

export function SendIcon({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <line x1="12" y1="19" x2="12" y2="5" />
      <polyline points="5 12 12 5 19 12" />
    </svg>
  );
}

export function CancelIcon({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <rect x="4" y="4" width="16" height="16" rx="2.5" />
    </svg>
  );
}

export function SpinnerIcon({ size = 14 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" style={{ animation: "spin 1s linear infinite" }} aria-hidden="true">
      <path d="M21 12a9 9 0 1 1-6.219-8.56" />
    </svg>
  );
}

export function PencilIcon({ size = 13 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M12 20h9" />
      <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
    </svg>
  );
}
