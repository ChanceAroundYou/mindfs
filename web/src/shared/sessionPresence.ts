/**
 * 会话 presence 三态：自跑（实心）> 委托中（空心）> 全闲（灭）。
 *
 * 背景：父会话把任务交给子代理后，父 turn 结束（pending 灭）但子仍在跑
 * （pending 亮）—— 只有亮/灭两态时父只能灭，误以为完成。新增 delegating
 * 表达「自己闲 + 任一后代在跑」。
 *
 * fork 不算后代：后端 fork 的 parent_session_key 恒空（manager_test 钉死），
 * 这里再显式排除 source type=fork / fork_from 残留，前后端双保险。
 */

export type SessionPresence = "running" | "delegating" | "idle";

export type PresenceTreeItem = {
  key?: string;
  session_key?: string;
  parent_session_key?: string;
  source?: string;
  fork_from?: string;
};

function presenceKeyOf(item: PresenceTreeItem): string {
  return String((item as any)?.key || (item as any)?.session_key || "").trim();
}

function isForkItem(item: PresenceTreeItem): boolean {
  if (String((item as any)?.fork_from || "").trim()) return true;
  const source = String((item as any)?.source || "");
  if (!source) return false;
  if (source.includes('"type":"fork"') || source.includes('"type": "fork"')) return true;
  try {
    const parsed = JSON.parse(source) as { type?: unknown };
    return (parsed as any)?.type === "fork";
  } catch {
    return false;
  }
}

export function getSessionPresence(selfPending: boolean, descendantPending: boolean): SessionPresence {
  if (selfPending) return "running";
  if (descendantPending) return "delegating";
  return "idle";
}

/**
 * 列表级入口：一次算全表的 presence（O(n)，不是每张卡各算一遍）。
 *
 * 卡片逐张调用 presenceFromTree 会让列表渲染变成 O(n²)（会话列表在流式期间
 * 每 30ms 重渲染一次），所以列表侧统一在这里算好、盖到会话项上，卡片只读字段。
 */
export function computePresenceByKey(items: PresenceTreeItem[]): Record<string, SessionPresence> {
  const pending: Record<string, boolean> = {};
  const childrenByParent = new Map<string, string[]>();
  for (const item of items || []) {
    const key = presenceKeyOf(item);
    if (!key) continue;
    // fork 项自己仍要有 presence（它自己的 pending 由自己表达）；只是不算任何人的后代。
    pending[key] = !!(item as any)?.pending;
    if (isForkItem(item)) continue;
    const parent = String((item as any)?.parent_session_key || "").trim();
    if (!parent) continue;
    const bucket = childrenByParent.get(parent);
    if (bucket) bucket.push(key);
    else childrenByParent.set(parent, [key]);
  }
  const memo = new Map<string, boolean>();
  const visiting = new Set<string>();
  // 后代（不含自己）里有没有在跑的。
  const descendantRunning = (key: string): boolean => {
    const cached = memo.get(key);
    if (cached !== undefined) return cached;
    if (visiting.has(key)) return false; // 脏数据成环：按「无」处理，不递归下去
    visiting.add(key);
    let running = false;
    for (const child of childrenByParent.get(key) || []) {
      if (pending[child] || descendantRunning(child)) {
        running = true;
        break;
      }
    }
    visiting.delete(key);
    memo.set(key, running);
    return running;
  };
  const out: Record<string, SessionPresence> = {};
  for (const key of Object.keys(pending)) {
    out[key] = getSessionPresence(pending[key], descendantRunning(key));
  }
  return out;
}

/** 把 presence 盖到会话项上（与 pending 覆盖同一惯例：派生字段，不写回真相源）。 */
export function applyPresenceToItems<T extends PresenceTreeItem>(items: T[]): T[] {
  const table = computePresenceByKey(items || []);
  return (items || []).map((item) => {
    const key = presenceKeyOf(item);
    const presence = key ? table[key] : undefined;
    if (!presence || (item as any).presence === presence) return item;
    return { ...(item as any), presence } as T;
  });
}

/**
 * 从「同级会话列表」直接算某一项的 presence —— 单项查询入口（viewer 那类只有一条
 * 选中项、没有整表可盖的场景）。列表里逐张卡调用它会把渲染拖成 O(n²)，列表侧
 * 请改用 computePresenceByKey / applyPresenceToItems。
 */
export function presenceFromTree(
  selfKey: string,
  selfPending: boolean,
  items: PresenceTreeItem[],
  rootId = "",
): SessionPresence {
  const pendingByKey: Record<string, boolean> = {};
  for (const item of items || []) {
    const key = presenceKeyOf(item);
    if (key) pendingByKey[key] = !!(item as any)?.pending;
  }
  return getSessionPresence(
    !!selfPending,
    hasRunningDescendant(selfKey, items || [], pendingByKey, (_rootId, sessionKey) => sessionKey, rootId),
  );
}

/**
 * 任一后代在跑？后代收集按 parent_session_key（fork 天然不在其中 + 显式排除），
 * pending 命中经调用方给的 keyOf（node-scoped，见 scopeSessionKey）。
 */
export function hasRunningDescendant(
  parentKey: string,
  items: PresenceTreeItem[],
  pendingByKey: Record<string, boolean>,
  keyOf: (rootId: string, sessionKey: string) => string,
  rootId: string,
): boolean {
  const root = String(parentKey || "").trim();
  if (!root || !items || !pendingByKey || typeof keyOf !== "function") return false;
  const childrenByParent = new Map<string, string[]>();
  for (const item of items) {
    if (isForkItem(item)) continue;
    const parent = String((item as any)?.parent_session_key || "").trim();
    const key = presenceKeyOf(item);
    if (!parent || !key) continue;
    const bucket = childrenByParent.get(parent);
    if (bucket) bucket.push(key);
    else childrenByParent.set(parent, [key]);
  }
  const seen = new Set<string>([root]);
  const queue: string[] = [...(childrenByParent.get(root) || [])];
  while (queue.length > 0) {
    const key = String(queue.shift() || "");
    if (!key || seen.has(key)) continue;
    seen.add(key);
    if (!!pendingByKey[keyOf(rootId, key)]) return true;
    for (const child of childrenByParent.get(key) || []) {
      if (!seen.has(child)) queue.push(child);
    }
  }
  return false;
}
