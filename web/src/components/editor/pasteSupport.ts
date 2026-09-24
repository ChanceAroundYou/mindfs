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
