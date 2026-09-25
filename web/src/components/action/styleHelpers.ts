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

export { hexToRgbaApp as appAccentHexToRgba } from "../../app/taskIcons";

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
