export { sessionActivityAt } from "../../shared/sessionActivityAt";

export type PinAwareSessionItem = {
  key?: string;
  session_key?: string;
  root_id?: string;
  updated_at?: string;
  /** Client-only stream activity until a refreshed server timestamp catches up. */
  activity_at?: string;
  pinned_at?: string | null;
};

function sessionKey(item: PinAwareSessionItem): string {
  return String(item.key || item.session_key || "");
}

function timeValue(value: string | null | undefined): number {
  return Date.parse(String(value || "")) || 0;
}

export function sessionActivityTime(
  item: Pick<PinAwareSessionItem, "updated_at" | "activity_at">,
): number {
  return Math.max(timeValue(item.updated_at), timeValue(item.activity_at));
}

export function projectActivityTime(group: {
  latestSessionTime?: string;
  activity_at?: string;
  sessions: readonly Pick<PinAwareSessionItem, "updated_at" | "activity_at">[];
}): number {
  return group.sessions.reduce(
    (latest, item) => Math.max(latest, sessionActivityTime(item)),
    Math.max(timeValue(group.latestSessionTime), timeValue(group.activity_at)),
  );
}

type ProjectGroupActivity = {
  latestSessionTime?: string;
  activity_at?: string;
  sessions: readonly Pick<PinAwareSessionItem, "updated_at" | "activity_at">[];
};

/** Keep configured node order outside project pin and activity sorting. */
export function orderProjectGroupsByNode<T extends ProjectGroupActivity>(
  groups: readonly T[],
  nodeOrder: readonly string[],
  getNodeKey: (group: T) => string,
  isPinned: (group: T) => boolean,
  compareStable: (left: T, right: T) => number,
): T[] {
  const configured = new Set(nodeOrder.map((key) => String(key || "").trim()).filter(Boolean));
  const buckets = new Map<string, T[]>();
  const unknownOrder: string[] = [];
  groups.forEach((group, index) => {
    // Missing identity gets its own stable bucket; never compare it by recency
    // against another node whose identity is also unavailable.
    const nodeKey = String(getNodeKey(group) || "").trim() || `\u0000missing:${index}`;
    let bucket = buckets.get(nodeKey);
    if (!bucket) {
      bucket = [];
      buckets.set(nodeKey, bucket);
      if (!configured.has(nodeKey)) unknownOrder.push(nodeKey);
    }
    bucket.push(group);
  });

  const compareWithinNode = (left: T, right: T): number => {
    const pinOrder = Number(isPinned(right)) - Number(isPinned(left));
    if (pinOrder !== 0) return pinOrder;
    const recency = projectActivityTime(right) - projectActivityTime(left);
    return recency !== 0 ? recency : compareStable(left, right);
  };
  const orderedKeys = [
    ...new Set(nodeOrder.map((key) => String(key || "").trim()).filter(Boolean)),
    ...unknownOrder,
  ];
  return orderedKeys.flatMap((key) => (buckets.get(key) || []).slice().sort(compareWithinNode));
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
    const previous = byKey.get(scopedKey);
    const merged = { ...(previous || ({} as T)), ...item };
    // Keep a local streaming timestamp only while the persisted session row
    // still lags it. This makes list reloads monotonic without storing every
    // token/chunk in the database.
    const latestActivity = Math.max(
      timeValue(previous?.activity_at),
      timeValue(item.activity_at),
    );
    if (latestActivity > timeValue(merged.updated_at)) {
      merged.activity_at =
        timeValue(item.activity_at) >= timeValue(previous?.activity_at)
          ? item.activity_at
          : previous?.activity_at;
    } else {
      delete merged.activity_at;
    }
    byKey.set(scopedKey, merged);
  }
  return Array.from(byKey.values()).sort(compareSessionItems);
}

/**
 * 把某个项目的置顶快照盖到列表上，并重排。
 *
 * 双向：既**盖上**快照里的置顶，也**抹掉**快照里没有的置顶。
 *
 * 为什么需要「盖」：置顶的权威是**主节点**那张表，而 worker 上的
 * `/api/pins` 是 403（它是控制面），worker 自己的会话列表里因此永远没有
 * 置顶区。不从主节点把置顶盖过去，在 PC 的项目里置顶就等于没置顶。
 *
 * 为什么需要「抹」：会话库里那个退役的 pinned_at 列可能还有残留值，
 * 留着会让置顶看起来在、点了又不动。两边以快照为准。
 */
export function applyPinnedSnapshotToSessions<T extends PinAwareSessionItem>(
  items: T[],
  rootId: string,
  pinnedKeys: string[],
  pinnedAtByKey?: Map<string, string>,
): T[] {
  const pinned = new Set(pinnedKeys.map((key) => String(key || "").trim()).filter(Boolean));
  const next = items.map((item) => {
    if (String(item.root_id || "") !== rootId) {
      return item;
    }
    const key = sessionKey(item);
    if (!key) {
      return item;
    }
    if (!pinned.has(key)) {
      if (!item.pinned_at) return item;
      const copy = { ...item };
      delete copy.pinned_at;
      return copy;
    }
    const at = pinnedAtByKey?.get(key);
    if (at && item.pinned_at !== at) {
      // 置顶时间取主节点那份：反复点「置顶」不刷新时间戳，两边必须一致，
      // 否则列表排序会在刷新后自己跳一下。
      return { ...item, pinned_at: at };
    }
    return item;
  });
  return next.sort(compareSessionItems);
}

function compareSessionItems(left: PinAwareSessionItem, right: PinAwareSessionItem): number {
  const leftPinned = !!left.pinned_at;
  const rightPinned = !!right.pinned_at;
  if (leftPinned !== rightPinned) {
    return leftPinned ? -1 : 1;
  }
  const leftUpdated = sessionActivityTime(left);
  const rightUpdated = sessionActivityTime(right);
  if (leftUpdated !== rightUpdated) {
    return rightUpdated - leftUpdated;
  }
  return sessionKey(left).localeCompare(sessionKey(right));
}
