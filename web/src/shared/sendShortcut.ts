export type SendShortcut = {
  key: string;
  alt: boolean;
  ctrl: boolean;
  meta: boolean;
  shift: boolean;
};

const MODIFIER_KEYS = new Set(["Alt", "AltGraph", "Control", "Meta", "Shift"]);

function normalizeKey(key: string): string {
  if (key === " ") return "Space";
  return key.length === 1 ? key.toLowerCase() : key;
}

export function shortcutFromKeyboardEvent(event: KeyboardEvent): SendShortcut | null {
  if (MODIFIER_KEYS.has(event.key) || event.key === "Dead" || event.key === "Process" || event.key === "Unidentified") {
    return null;
  }
  const shortcut = {
    key: normalizeKey(event.key),
    alt: event.altKey,
    ctrl: event.ctrlKey,
    meta: event.metaKey,
    shift: event.shiftKey,
  };
  return isValidSendShortcut(shortcut) ? shortcut : null;
}

export function isValidSendShortcut(value: unknown): value is SendShortcut {
  if (!value || typeof value !== "object") return false;
  const shortcut = value as Partial<SendShortcut>;
  return typeof shortcut.key === "string"
    && shortcut.key.length > 0
    && typeof shortcut.alt === "boolean"
    && typeof shortcut.ctrl === "boolean"
    && typeof shortcut.meta === "boolean"
    && typeof shortcut.shift === "boolean"
    && (shortcut.alt || shortcut.ctrl || shortcut.meta);
}

export function matchesSendShortcut(event: KeyboardEvent, shortcut: SendShortcut | null): boolean {
  return !!shortcut
    && normalizeKey(event.key) === shortcut.key
    && event.altKey === shortcut.alt
    && event.ctrlKey === shortcut.ctrl
    && event.metaKey === shortcut.meta
    && event.shiftKey === shortcut.shift;
}

function displayKey(key: string): string {
  const labels: Record<string, string> = {
    ArrowDown: "↓",
    ArrowLeft: "←",
    ArrowRight: "→",
    ArrowUp: "↑",
    Backspace: "Backspace",
    Delete: "Delete",
    Enter: "Enter",
    Escape: "Esc",
    Space: "Space",
    Tab: "Tab",
  };
  return labels[key] || (key.length === 1 ? key.toUpperCase() : key);
}

/**
 * 回车键的语义：`send` 发送，`newline` 换行（交回编辑器默认行为）。
 *
 * 会话输入框与任务输入框（新建 / 详情）共用这一处判断，避免两套键盘行为各改各的：
 *
 * - 桌面：Enter 换行，Ctrl/Cmd+Enter 发送；Shift+Enter 也换行。
 * - 移动端软键盘没有 Ctrl：按「回车键发送」设置决定，默认回车换行、右下角发送键发送
 *   （聊天软键盘的惯例），所以发送永远可用。
 * - 用户自己录的发送快捷键（`sendShortcut`）在两端都优先，与内置的 Ctrl/Cmd+Enter 并存。
 */
export type EnterIntent = "send" | "newline";

export type EnterIntentOptions = {
  /** 手机 / 窄屏布局（软键盘没有修饰键）。 */
  isMobile: boolean;
  /** 移动端设置项「回车键发送」；桌面端不看它。 */
  mobileEnterKeySends: boolean;
  /** 用户自选的发送快捷键。 */
  sendShortcut?: SendShortcut | null;
};

export function resolveEnterIntent(event: KeyboardEvent | null, options: EnterIntentOptions): EnterIntent {
  if (!event) return "newline";
  if (event.key !== "Enter" && event.key !== "NumpadEnter") return "newline";
  if (matchesSendShortcut(event, options.sendShortcut ?? null)) return "send";
  // 内置发送键：Ctrl/Cmd+Enter（移动端接外接键盘同样可用）。
  if (event.ctrlKey || event.metaKey) return "send";
  if (event.shiftKey || event.altKey) return "newline";
  if (!options.isMobile) return "newline";
  return options.mobileEnterKeySends ? "send" : "newline";
}

export function formatSendShortcut(shortcut: SendShortcut): string {
  const parts: string[] = [];
  if (shortcut.ctrl) parts.push("Ctrl");
  if (shortcut.alt) parts.push("Alt");
  if (shortcut.shift) parts.push("Shift");
  if (shortcut.meta) parts.push("⌘");
  parts.push(displayKey(shortcut.key));
  return parts.join("+");
}
