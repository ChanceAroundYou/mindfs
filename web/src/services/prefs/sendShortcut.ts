import { getStoredString, removeStoredString, setStoredString } from "./storage";
import { isValidSendShortcut, type SendShortcut } from "../../shared/sendShortcut";
export {
  formatSendShortcut,
  isValidSendShortcut,
  matchesSendShortcut,
  shortcutFromKeyboardEvent,
} from "../../shared/sendShortcut";
export type { SendShortcut } from "../../shared/sendShortcut";

export const SEND_SHORTCUT_STORAGE_KEY = "mindfs-send-shortcut";

export function loadSendShortcut(): SendShortcut | null {
  const raw = getStoredString(SEND_SHORTCUT_STORAGE_KEY);
  if (!raw) return null;
  try {
    const parsed: unknown = JSON.parse(raw);
    return isValidSendShortcut(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

export function persistSendShortcut(shortcut: SendShortcut | null): void {
  if (shortcut) {
    setStoredString(SEND_SHORTCUT_STORAGE_KEY, JSON.stringify(shortcut));
  } else {
    removeStoredString(SEND_SHORTCUT_STORAGE_KEY);
  }
}
