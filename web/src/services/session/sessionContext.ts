// 上下文窗口的 selection 去重（从 services/session.ts 抽出，2026-10-07）。
//
// 服务端 buildUserPrompt 只用**当前这条**消息的 selection，所以同一条会话里
// 连续几条消息带同一个 selection 时，后面的可以省掉（省字节）。判等键是
// file+行号+文本 —— 漏任一项会把「同一个文件的不同选区」误判成没变。
export function buildSelectionKey(selection: unknown): string {
  if (!selection || typeof selection !== "object") return "";
  const raw = selection as Record<string, unknown>;
  const filePath = typeof raw.file_path === "string" ? raw.file_path : "";
  const startLine = typeof raw.start_line === "number" ? raw.start_line : -1;
  const endLine = typeof raw.end_line === "number" ? raw.end_line : -1;
  const text = typeof raw.text === "string" ? raw.text : "";
  return `${filePath}:${startLine}:${endLine}:${text}`;
}

export function compactContext(
  contextCache: Map<string, { selectionKey: string }>,
  sessionKey: string | undefined,
  context?: Record<string, unknown>,
): Record<string, unknown> | undefined {
  if (!context) return undefined;
  const next = { ...context };
  const selection =
    next.selection && typeof next.selection === "object"
      ? (next.selection as Record<string, unknown>)
      : undefined;
  const selectionKey = buildSelectionKey(selection);

  if (sessionKey) {
    const prev = contextCache.get(sessionKey);
    if (prev && prev.selectionKey === selectionKey) {
      delete next.selection;
    }
    contextCache.set(sessionKey, { selectionKey });
  }
  return next;
}
