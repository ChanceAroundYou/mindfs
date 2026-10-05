import React from "react";
import { rootBadgeButtonStyle, rootBadgeStyle } from "./rootBadgeStyle";

function badgeBg(_color: string): string {
  return "var(--node-badge-bg)";
}

export function NodeBadgeHeader({
  color,
  label,
  collapsed = false,
  onClick,
  notice,
}: {
  color: string;
  label: string;
  collapsed?: boolean;
  onClick?: () => void;
  /**
   * 分组头的故障标记（如节点连不上）。收成一个红色叹号，点了才弹说明 ——
   * 说明文字原来常驻在标题旁边，一屏几十个项目时噪声比信息量大得多，
   * 而「有没有问题」一眼扫得到就够了。
   */
  notice?: string;
}) {
  const c = String(color || "#6d5bcf").trim() || "#6d5bcf";
  // 提示气泡只在展开时存在，用 ref + 全局 pointerdown 收口：点在别处就消失。
  // 不引新组件 —— 仓库里没有可复用的 popover 原语，为此加一个不划算。
  const [noticeOpen, setNoticeOpen] = React.useState(false);
  const noticeRef = React.useRef<HTMLSpanElement | null>(null);
  React.useEffect(() => {
    if (!noticeOpen) return;
    const onDown = (e: PointerEvent) => {
      if (!noticeRef.current?.contains(e.target as Node)) setNoticeOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setNoticeOpen(false);
    };
    document.addEventListener("pointerdown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [noticeOpen]);
  return (
    <div
      style={{
        minWidth: 0,
        height: "22px",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        gap: "8px",
        padding: "0 24px 0 2px",
        background: "transparent",
        boxSizing: "border-box",
        position: "relative",
      }}
    >
      <span aria-hidden="true" style={{ height: "1px", flex: 1, minWidth: "12px", background: "var(--border-color)" }} />
      {onClick ? (
        <button
          type="button"
          onClick={onClick}
          style={{
            ...rootBadgeButtonStyle,
            background: badgeBg(c),
            flexShrink: 1,
            maxWidth: "100%",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
            cursor: "pointer",
            display: "inline-flex",
            alignItems: "center",
            gap: "5px",
          }}
        >
          <svg
            width="12"
            height="12"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2.5"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
            style={{
              flexShrink: 0,
              transform: collapsed ? "rotate(0deg)" : "rotate(90deg)",
              transition: "transform 0.15s cubic-bezier(0.4, 0, 0.2, 1)",
              color: c,
            }}
          >
            <polyline points="9 18 15 12 9 6" />
          </svg>
          <span style={{ color: c, fontWeight: 600 }}>{label}</span>
        </button>
      ) : (
        <span
          style={{
            ...rootBadgeStyle,
            background: badgeBg(c),
            flexShrink: 1,
            maxWidth: "100%",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          <span style={{ color: c, fontWeight: 600 }}>{label}</span>
        </span>
      )}
      {notice ? (
        <span ref={noticeRef} style={{ position: "relative", flexShrink: 0, display: "inline-flex" }}>
          <button
            type="button"
            aria-label={notice}
            aria-expanded={noticeOpen}
            onClick={() => setNoticeOpen((v) => !v)}
            style={{
              width: "15px",
              height: "15px",
              padding: 0,
              border: "none",
              borderRadius: "50%",
              background: "var(--danger-color, #dc2626)",
              color: "#fff",
              fontSize: "10px",
              fontWeight: 800,
              lineHeight: 1,
              display: "inline-flex",
              alignItems: "center",
              justifyContent: "center",
              cursor: "pointer",
              flexShrink: 0,
            }}
          >
            !
          </button>
          {noticeOpen ? (
            <span
              role="tooltip"
              style={{
                position: "absolute",
                top: "calc(100% + 6px)",
                right: 0,
                zIndex: 20,
                width: "max-content",
                maxWidth: "240px",
                padding: "7px 9px",
                borderRadius: "8px",
                border: "1px solid var(--border-color)",
                background: "var(--panel-bg)",
                color: "var(--text-primary)",
                boxShadow: "0 10px 24px rgba(15, 23, 42, 0.18)",
                fontSize: "11px",
                fontWeight: 500,
                lineHeight: 1.5,
                whiteSpace: "normal",
                textAlign: "left",
              }}
            >
              {notice}
            </span>
          ) : null}
        </span>
      ) : null}
      <span aria-hidden="true" style={{ height: "1px", flex: 1, minWidth: "12px", background: "var(--border-color)" }} />
    </div>
  );
}
