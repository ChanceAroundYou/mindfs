import { useEffect, useState } from "react";
import { type MessageKey } from "../../i18n";
import { type WSStatus } from "./types";

export const MOBILE_BREAKPOINT = 768;
export const IME_ENTER_GUARD_MS = 120;
export const CANDIDATE_FETCH_DEBOUNCE_MS = 512;

export const modePlaceholderKeys: Record<string, MessageKey> = {
  chat: "action.placeholder.chat",
  plugin: "action.placeholder.plugin",
  command: "action.placeholder.command",
};

export const chatBlurPlaceholderKeys: MessageKey[] = [
  "action.placeholder.chat",
  "action.placeholder.tip",
];

export function wsStatusMeta(status: WSStatus, t: (key: MessageKey) => string): {
  color: string;
  shadow: string;
  label: string;
} {
  switch (status) {
    case "connected":
      return {
        color: "#22c55e",
        shadow: "none",
        label: t("action.ws.connected"),
      };
    case "connecting":
      return {
        color: "#f59e0b",
        shadow: "none",
        label: t("action.ws.connecting"),
      };
    case "reconnecting":
      return {
        color: "#ef4444",
        shadow: "none",
        label: t("action.ws.reconnecting"),
      };
    case "disconnected":
    default:
      return {
        color: "#94a3b8",
        shadow: "none",
        label: t("action.ws.disconnected"),
      };
  }
}

export function getSelectionPreview(text?: string): string {
  const trimmed = String(text || "").trim();
  if (!trimmed) {
    return "...";
  }
  return `${Array.from(trimmed).slice(0, 3).join("")}...`;
}

export function appAccentHexToRgba(hex: string, alpha: number): string {
  const h = String(hex || "").trim().replace(/^#/, "");
  const fallback = `rgba(37, 99, 235, ${alpha})`;
  if (h.length === 3) { const r = parseInt(h[0] + h[0], 16); const g = parseInt(h[1] + h[1], 16); const b = parseInt(h[2] + h[2], 16); if (Number.isFinite(r) && Number.isFinite(g) && Number.isFinite(b)) return `rgba(${r}, ${g}, ${b}, ${alpha})`; return fallback; }
  if (h.length === 6) { const r = parseInt(h.slice(0, 2), 16); const g = parseInt(h.slice(2, 4), 16); const b = parseInt(h.slice(4, 6), 16); if (Number.isFinite(r) && Number.isFinite(g) && Number.isFinite(b)) return `rgba(${r}, ${g}, ${b}, ${alpha})`; }
  if (/^rgba?\(/.test(String(hex || ""))) return String(hex);
  return fallback;
}

export function useResponsive() {
  const [isMobile, setIsMobile] = useState(false);
  useEffect(() => {
    const checkSize = () => {
      setIsMobile(window.innerWidth < MOBILE_BREAKPOINT);
    };
    checkSize();
    window.addEventListener("resize", checkSize);
    return () => window.removeEventListener("resize", checkSize);
  }, []);
  return { isMobile };
}
