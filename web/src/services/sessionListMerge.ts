export type PinAwareSessionItem = {
  key?: string;
  session_key?: string;
  root_id?: string;
  updated_at?: string;
  pinned_at?: string | null;
};

function sessionKey(item: PinAwareSessionItem): string {
  return String(item.key || item.session_key || "");
}

function timeValue(value: string | null | undefined): number {
  return Date.parse(String(value || "")) || 0;
}

export function mergeSessionItems<T extends PinAwareSessionItem>(
  current: T[],
  incoming: T[],
): T[] {
  const byKey = new Map<string, T>();
  for (const item of current) {
    const key = sessionKey(item);
    if (!key) continue;
    // C3: 归并键按 _nodeId 作用域，跨节点同 key 会话各自保留，不互相覆盖
    byKey.set(`${String((item as any)?._nodeId || "").trim()}::${key}`, item);
  }
  for (const item of incoming) {
    const key = sessionKey(item);
    if (!key) {
      continue;
    }
    const scopedKey = `${String((item as any)?._nodeId || "").trim()}::${key}`;
    const prevItem = byKey.get(scopedKey);
    if (prevItem) {
      const prevNid = String((prevItem as any)?._nodeId || "").trim();
      const incomingNid = String((item as any)?._nodeId || "").trim();
      if (prevNid && incomingNid && prevNid !== incomingNid) {
        console.info("[session-list] merge override", { key, prevNid, incomingNid });
      }
    }
    byKey.set(scopedKey, { ...(byKey.get(scopedKey) || ({} as T)), ...item });
  }
  return Array.from(byKey.values()).sort(compareSessionItems);
}

export function applyPinnedSnapshotToSessions<T extends PinAwareSessionItem>(
  items: T[],
  rootId: string,
  pinnedKeys: string[],
): T[] {
  const pinned = new Set(pinnedKeys.map((key) => String(key || "").trim()).filter(Boolean));
  const next = items.map((item) => {
    if (String(item.root_id || "") !== rootId) {
      return item;
    }
    const key = sessionKey(item);
    if (!key || pinned.has(key) || !item.pinned_at) {
      return item;
    }
    const copy = { ...item };
    delete copy.pinned_at;
    return copy;
  });
  return next.sort(compareSessionItems);
}

function compareSessionItems(left: PinAwareSessionItem, right: PinAwareSessionItem): number {
  const leftPinned = timeValue(left.pinned_at);
  const rightPinned = timeValue(right.pinned_at);
  if (leftPinned || rightPinned) {
    if (leftPinned !== rightPinned) {
      return rightPinned - leftPinned;
    }
  }
  const leftUpdated = timeValue(left.updated_at);
  const rightUpdated = timeValue(right.updated_at);
  if (leftUpdated !== rightUpdated) {
    return rightUpdated - leftUpdated;
  }
  return sessionKey(left).localeCompare(sessionKey(right));
}
