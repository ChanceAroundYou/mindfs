function getPasteDataTransfer(event: ClipboardEvent | InputEvent | KeyboardEvent): DataTransfer | null {
  if (typeof ClipboardEvent !== "undefined" && event instanceof ClipboardEvent) {
    return event.clipboardData;
  }
  if (typeof InputEvent !== "undefined" && event instanceof InputEvent) {
    return event.dataTransfer;
  }
  return null;
}

export function getPlainTextFromPasteEvent(event: ClipboardEvent | InputEvent | KeyboardEvent): string {
  const dataTransfer = getPasteDataTransfer(event);
  return dataTransfer?.getData("text/plain") || dataTransfer?.getData("text/uri-list") || "";
}

export function pasteEventHasFiles(event: ClipboardEvent | InputEvent | KeyboardEvent): boolean {
  const dataTransfer = getPasteDataTransfer(event);
  return Array.from(dataTransfer?.items || []).some((item) => item.kind === "file");
}

/**
 * 从粘贴事件里取图片文件（截图/复制的图片）。
 *
 * 会话输入框、新建任务面板、任务详情输入框共用这一处：只要 `kind === "file"`
 * 且 MIME 是 image/* 就走「附件」链路，纯文本粘贴仍交给编辑器自己处理。
 * React 的 ClipboardEvent 与原生事件都带 `clipboardData`，所以形状按结构类型收。
 */
export function getClipboardImageFiles(event: { clipboardData?: DataTransfer | null }): File[] {
  const items = Array.from(event.clipboardData?.items || []);
  return items
    .filter((item) => item.kind === "file" && item.type.startsWith("image/"))
    .map((item) => item.getAsFile())
    .filter((file): file is File => !!file);
}

export function isKeyboardPasteInput(event: InputEvent): boolean {
  const data = event.data || "";
  return event.inputType === "insertFromPaste"
    || event.inputType === "insertFromPasteAsQuotation"
    || !!event.dataTransfer
    || data.includes("\n")
    || data.includes("\r");
}

export async function readClipboardTextFallback(): Promise<string> {
  try {
    const mod = await import("@capacitor/clipboard");
    const result = await mod.Clipboard.read();
    if (result.value) {
      return result.value;
    }
  } catch {
    // Fall through to the browser clipboard API.
  }
  try {
    return await navigator.clipboard?.readText?.() || "";
  } catch {
    return "";
  }
}
