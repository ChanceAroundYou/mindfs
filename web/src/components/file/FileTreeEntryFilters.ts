import type { FileEntry } from "../../services/prefs/directorySort";
export function childEntryKey(entry: FileEntry, entryRoot: string, nodeId: string, treeKey: (node: string, root: string, path: string) => string) {
  return entry.is_root ? treeKey(nodeId, entry.path, ".") : treeKey(nodeId, entryRoot, entry.path);
}
export function filterVisibleEntries(items: FileEntry[], depth: number, tab: string, rootId: string | null | undefined, showHidden: boolean) {
  const filtered = showHidden ? items : items.filter((entry) => !entry.name.startsWith("."));
  return tab !== "related" || depth !== 0 ? filtered : filtered.filter((entry) => !!rootId && entry.path === rootId);
}
