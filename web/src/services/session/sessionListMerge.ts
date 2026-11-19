
export type PinAwareSessionItem = {
  key?: string;
  session_key?: string;
  root_id?: string;
  updated_at?: string;
  pinned_at?: string | null;
  /** 服务端 `/api/replying-sessions` 的旁路真值，由前端盖上（见 appSession.applyPendingToMultiProjectGroups）。 */
  pending?: boolean;
};

/**
 * 项目/会话按活动时间重排的迟滞阈值（毫秒）。
 *
 * 两个都在跑的会话会每 100ms 交替推进 `updated_at`（本节点 stream 事件），若比较器按毫秒
 * 直接比大小，列表会疯狂交替抖动。差距小于该阈值时视为"同时在跑"，
 * 保持原有相对顺序（fallback 到稳定的 compareStable），不重排。
 */
export const ACTIVITY_REORDER_HYSTERESIS_MS = 30_000;

function withinHysteresis(leftTime: number, rightTime: number): boolean {
  return Math.abs(leftTime - rightTime) < ACTIVITY_REORDER_HYSTERESIS_MS;
}

function sessionKey(item: PinAwareSessionItem): string {
  return String(item.key || item.session_key || "");
}

function timeValue(value: string | null | undefined): number {
  return Date.parse(String(value || "")) || 0;
}

export function sessionActivityTime(
  item: Pick<PinAwareSessionItem, "updated_at">,
): number {
  return timeValue(item.updated_at);
}

export function projectActivityTime(group: {
  latestSessionTime?: string;
  sessions: readonly Pick<PinAwareSessionItem, "updated_at">[];
}): number {
  return group.sessions.reduce(
    (latest, item) => Math.max(latest, sessionActivityTime(item)),
    timeValue(group.latestSessionTime),
  );
}

type ProjectGroupActivity = {
  latestSessionTime?: string;
  sessions: readonly Pick<PinAwareSessionItem, "updated_at">[];
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
    const leftTime = projectActivityTime(left);
    const rightTime = projectActivityTime(right);
    if (!withinHysteresis(leftTime, rightTime) && rightTime !== leftTime) {
      return rightTime - leftTime;
    }
    return compareStable(left, right);
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
    byKey.set(scopedKey, merged);
  }
  return Array.from(byKey.values()).sort(compareSessionItems);
}

/**
 * 把控制面下发的置顶快照盖回列表项，并按现行比较器重排。
 *
 * 重排必须走 `compareSessionItems`：调用方（全量重拉、置顶快照回填）拿到的条目
 * 可能带着 `pending`，若只排序不认它，正在回复的会话会被打回服务端 `updated_at`
 * 决定的旧位置。`pending` 是旁路真值、不随服务端 payload 下发，重拉后由
 * `applyPendingToMultiProjectGroups`（多项目）或 `sessionsWithPresence`（单项目）
 * 重新盖上；本函数不负责继承，也不要在这里另写一套"新旧判定"。
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
  // 「正在回复」是硬分桶：服务端 updated_at 是最后一条对话消息的落库时间，
  // agent 连跑十分钟工具时它一直停在起跑时刻，只按它排会让在跑的会话被刚完成的压下去。
  // pending 来自 /api/replying-sessions（轮询 + 本节点实时流），是「它现在还在跑」的真值。
  const leftPending = !!left.pending;
  const rightPending = !!right.pending;
  if (leftPending !== rightPending) {
    return leftPending ? -1 : 1;
  }
  const leftUpdated = sessionActivityTime(left);
  const rightUpdated = sessionActivityTime(right);
  if (rightUpdated !== leftUpdated && !withinHysteresis(leftUpdated, rightUpdated)) {
    return rightUpdated - leftUpdated;
  }
  return sessionKey(left).localeCompare(sessionKey(right));
}
