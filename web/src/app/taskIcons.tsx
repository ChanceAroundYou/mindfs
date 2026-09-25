import React from "react";
import { type MessageKey } from "../i18n";
import { type MessageParams } from "../i18n";

export function ImportIcon() {
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="currentColor"
      aria-hidden="true"
    >
      <path d="m14 12l-4-4v3H2v2h8v3m10 2V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v3h2V6h12v12H6v-3H4v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2" />
    </svg>
  );
}

export function EditPencilIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path
        d="M4 20h4.2L18.7 9.5a2.1 2.1 0 0 0 0-3L17.5 5.3a2.1 2.1 0 0 0-3 0L4 15.8V20Z"
        stroke="currentColor"
        strokeWidth="1.8"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <path
        d="m13.5 6.3 4.2 4.2"
        stroke="currentColor"
        strokeWidth="1.8"
        strokeLinecap="round"
      />
    </svg>
  );
}

export function HorizontalDotsIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <circle cx="5" cy="12" r="1.8" />
      <circle cx="12" cy="12" r="1.8" />
      <circle cx="19" cy="12" r="1.8" />
    </svg>
  );
}

export function PlusSmallIcon() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" aria-hidden="true">
      <path d="M12 5v14" />
      <path d="M5 12h14" />
    </svg>
  );
}

export function CheckIconSmall() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path d="m5 12 4 4L19 6" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function TaskCompleteIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 16 16" aria-hidden="true">
      <path d="M0 0h16v16H0z" fill="none" />
      <path fill="currentColor" fillRule="evenodd" d="M3 13.5a.5.5 0 0 1-.5-.5V3a.5.5 0 0 1 .5-.5h9.25a.75.75 0 0 0 0-1.5H3a2 2 0 0 0-2 2v10a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V9.75a.75.75 0 0 0-1.5 0V13a.5.5 0 0 1-.5.5zm12.78-8.82a.75.75 0 0 0-1.06-1.06L9.162 9.177 7.289 7.241a.75.75 0 1 0-1.078 1.043l2.403 2.484a.75.75 0 0 0 1.07.01z" clipRule="evenodd" />
    </svg>
  );
}

export function TaskQueuedSpinnerIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.5"
      strokeLinecap="round"
      aria-hidden="true"
      style={{ animation: "mindfs-update-spin 0.9s linear infinite" }}
    >
      <path d="M21 12a9 9 0 1 1-6.2-8.56" />
    </svg>
  );
}

export function TaskPauseIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" style={{ display: "block" }}>
      <rect x="6" y="5" width="4" height="14" rx="1.2" />
      <rect x="14" y="5" width="4" height="14" rx="1.2" />
    </svg>
  );
}

export function TaskResumeIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="16"
      height="16"
      viewBox="0 0 24 24"
      aria-hidden="true"
      style={{ display: "block" }}
    >
      <path d="M0 0h24v24H0z" fill="none" />
      <path
        fill="currentColor"
        d="M14.5 4h.005M14.5 4L12 10l5 2.898L9.5 20l2.5-6l-5-2.9zm0-2a2.02 2.02 0 0 0-1.379.551L5.624 9.646a2 2 0 0 0-.61 1.686c.072.626.437 1.182.982 1.498l3.482 2.021l-1.826 4.381a2.003 2.003 0 0 0 1.847 2.77c.498 0 .993-.186 1.375-.548l7.5-7.103a2 2 0 0 0 .61-1.685a2 2 0 0 0-.982-1.498L14.52 9.15l1.789-4.293A2 2 0 0 0 14.5 2"
      />
    </svg>
  );
}

export function TaskRunNowIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="16"
      height="16"
      viewBox="0 0 24 24"
      aria-hidden="true"
      style={{ display: "block", transform: "translateX(-1px) scale(1.06)", transformOrigin: "center" }}
    >
      <path d="M0 0h24v24H0z" fill="none" />
      <path
        fill="currentColor"
        d="M14.5 4h.005M14.5 4L12 10l5 2.898L9.5 20l2.5-6l-5-2.9zm0-2a2.02 2.02 0 0 0-1.379.551L5.624 9.646a2 2 0 0 0-.61 1.686c.072.626.437 1.182.982 1.498l3.482 2.021l-1.826 4.381a2.003 2.003 0 0 0 1.847 2.77c.498 0 .993-.186 1.375-.548l7.5-7.103a2 2 0 0 0 .61-1.685a2 2 0 0 0-.982-1.498L14.52 9.15l1.789-4.293A2 2 0 0 0 14.5 2"
      />
    </svg>
  );
}

export function TaskSessionErrorIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v6" />
      <path d="M12 17h.01" />
    </svg>
  );
}

export function DeleteIcon() {
  return (
    <svg
      width="13"
      height="13"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <polyline points="3 6 5 6 21 6" />
      <path d="M19 6l-1 14H6L5 6" />
      <path d="M10 11v6" />
      <path d="M14 11v6" />
      <path d="M9 6V4h6v2" />
    </svg>
  );
}

export function taskTemplateMenuItemStyle(disabled = false): React.CSSProperties {
  return {
    width: "100%",
    minHeight: "30px",
    border: "none",
    borderRadius: "8px",
    background: "transparent",
    color: "var(--text-primary)",
    display: "flex",
    alignItems: "center",
    gap: "8px",
    padding: "6px 8px",
    textAlign: "left",
    fontSize: "12px",
    cursor: disabled ? "not-allowed" : "pointer",
    opacity: disabled ? 0.45 : 1,
    boxSizing: "border-box",
  };
}

export function taskCardIconButtonStyle(tone: "default" | "accent" | "success" | "danger" | "warning" = "default"): React.CSSProperties {
  return {
    width: "22px",
    height: "22px",
    border: "none",
    borderRadius: "6px",
    background: "transparent",
    color: tone === "accent" ? "var(--accent-color)" : tone === "success" ? "#16a34a" : tone === "danger" ? "#dc2626" : tone === "warning" ? "#d97706" : "var(--text-secondary)",
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    cursor: "pointer",
    padding: 0,
  };
}

export function taskAuxBadgeStyle(attention = false): React.CSSProperties {
  return {
    width: "18px",
    height: "18px",
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    borderRadius: "5px",
    background: attention ? "rgba(239, 68, 68, 0.10)" : "transparent",
    color: "var(--text-secondary)",
    animation: attention ? "mindfs-task-ask-user-pulse 2.2s ease-in-out infinite" : "none",
  };
}

export function taskWorktreeTagStyle(enabled: boolean): React.CSSProperties {
  return {
    flex: "0 0 auto",
    marginLeft: "auto",
    display: "inline-flex",
    alignItems: "center",
    gap: "1px",
    border: enabled ? "1px solid rgba(22, 163, 74, 0.28)" : "1px solid rgba(217, 119, 6, 0.28)",
    borderRadius: "4px",
    background: enabled ? "rgba(22, 163, 74, 0.08)" : "rgba(217, 119, 6, 0.08)",
    color: enabled ? "#15803d" : "#b45309",
    fontSize: "9px",
    fontWeight: 800,
    lineHeight: "12px",
    padding: enabled ? "0 4px" : "0 3px 0 2px",
  };
}

export function hexToRgbaApp(hex: string, alpha: number): string {
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

export function taskReplyPulseStyle(color?: string | null): React.CSSProperties {
  const c = String(color || "#2563eb").trim() || "#2563eb";
  return {
    position: "absolute",
    top: "6px",
    right: "6px",
    width: "8px",
    height: "8px",
    borderRadius: "999px",
    boxSizing: "border-box",
    border: `1.5px solid ${c}`,
    background: c,
    animation: "mindfs-bound-pulse 2.2s ease-in-out infinite",
    boxShadow: `0 0 0 1.5px ${hexToRgbaApp(c, 0.14)}`,
    pointerEvents: "none",
  };
}

export function TaskPlanAuxIcon({ color }: { color?: string } = {}) {
  const c = String(color || "#2563eb").trim() || "#2563eb";
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path d="M7 6h10M7 12h10M7 18h6" stroke={c} strokeWidth="2" strokeLinecap="round" />
      <path d="M4 6h.01M4 12h.01M4 18h.01" stroke={c} strokeWidth="3" strokeLinecap="round" />
    </svg>
  );
}

export function TaskExpandIcon({ collapsed }: { collapsed: boolean }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="13"
      height="13"
      viewBox="0 0 16 16"
      aria-hidden="true"
      style={{
        display: "block",
        transform: collapsed ? "none" : "rotate(180deg)",
      }}
    >
      <path d="M0 0h16v16H0z" fill="none" />
      <path fill="currentColor" d="M12.146 7.146a.5.5 0 0 1 .708.708l-4.5 4.5a.5.5 0 0 1-.708 0l-4.5-4.5a.5.5 0 1 1 .708-.708L8 11.293zm0-4a.5.5 0 0 1 .708.708l-4.5 4.5a.5.5 0 0 1-.708 0l-4.5-4.5a.5.5 0 1 1 .708-.708L8 7.293z" />
    </svg>
  );
}

export function TaskGroupChevronIcon({ collapsed }: { collapsed: boolean }) {
  return (
    <span
      aria-hidden="true"
      style={{
        flexShrink: 0,
        transform: collapsed ? "rotate(0deg)" : "rotate(90deg)",
        transition: "transform 0.2s",
        color: "currentColor",
        display: "inline-flex",
        alignItems: "center",
      }}
    >
      <svg
        width="12"
        height="12"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2.25"
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        <polyline points="9 18 15 12 9 6" />
      </svg>
    </span>
  );
}

export function RunNowIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="13"
      height="13"
      viewBox="0 0 24 24"
      aria-hidden="true"
    >
      <path d="M0 0h24v24H0z" fill="none" />
      <path
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        d="M20.409 9.353a2.998 2.998 0 0 1 0 5.294L7.597 21.614C5.534 22.737 3 21.277 3 18.968V5.033c0-2.31 2.534-3.769 4.597-2.648z"
      />
    </svg>
  );
}

export function SyncIcon({ style }: { style?: React.CSSProperties }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="13"
      height="13"
      viewBox="0 0 24 24"
      aria-hidden="true"
      style={style}
    >
      <path
        fill="currentColor"
        d="M19.91 15.51h-4.53a1 1 0 0 0 0 2h2.4A8 8 0 0 1 4 12a1 1 0 0 0-2 0a10 10 0 0 0 16.88 7.23V21a1 1 0 0 0 2 0v-4.5a1 1 0 0 0-.97-.99M12 2a10 10 0 0 0-6.88 2.77V3a1 1 0 0 0-2 0v4.5a1 1 0 0 0 1 1h4.5a1 1 0 0 0 0-2h-2.4A8 8 0 0 1 20 12a1 1 0 0 0 2 0A10 10 0 0 0 12 2"
      />
    </svg>
  );
}

export function ChevronDownSmallIcon() {
  return (
    <svg
      width="12"
      height="12"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="m6 9 6 6 6-6" />
    </svg>
  );
}
