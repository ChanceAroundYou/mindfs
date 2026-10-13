import { getNodes } from "../../services/net/nodeRegistry";
import { groupEntriesByNode } from "./FileTreeEntryFilters";
import type { FileEntry } from "../../shared/directorySort";

export function useFileTreeNodeGroups(entries: FileEntry[]) {
  return groupEntriesByNode(entries, getNodes);
}
