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

/** 发送按钮：实心 accent 底，禁用时降透明度。取消态传 cancel。 */
export function composerSendButtonStyle(opts: {
  enabled?: boolean;
  cancel?: boolean;
  cancelling?: boolean;
  busy?: boolean;
} = {}): React.CSSProperties {
  const { enabled = true, cancel = false, cancelling = false } = opts;
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
