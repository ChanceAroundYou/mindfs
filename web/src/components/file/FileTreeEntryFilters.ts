import type { FileEntry } from "../../services/prefs/directorySort";
export function childEntryKey(entry: FileEntry, entryRoot: string, nodeId: string, treeKey: (node: string, root: string, path: string) => string) {
  return entry.is_root ? treeKey(nodeId, entry.path, ".") : treeKey(nodeId, entryRoot, entry.path);
}
export function filterVisibleEntries(items: FileEntry[], depth: number, tab: string, rootId: string | null | undefined, showHidden: boolean) {
  const filtered = showHidden ? items : items.filter((entry) => !entry.name.startsWith("."));
  return tab !== "related" || depth !== 0 ? filtered : filtered.filter((entry) => !!rootId && entry.path === rootId);
}

export function groupEntriesByNode(entries: FileEntry[], getNodes: () => any[]) {
  const m = new Map<string, { color: string; name: string; nid: string; items: FileEntry[] }>();
  for (const entry of entries) {
    const ex = entry as any; const key = String(ex._nodeName || ex._nodeId || "local");
    if (!m.has(key)) m.set(key, { color: String(ex._nodeColor || "#6d5bcf"), name: String(ex._nodeName || key), nid: String(ex._nodeId || "").trim(), items: [] });
    m.get(key)!.items.push(entry);
  }
  try { const order = new Map(getNodes().map((n: any, i: number) => [String(n.name), i] as const)); return Array.from(m.entries()).sort((a,b) => (order.get(a[0]) ?? 99) - (order.get(b[0]) ?? 99)).map(([,v]) => v); } catch { return Array.from(m.values()); }
}
