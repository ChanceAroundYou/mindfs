/**
 * 会话 presence 四态：自跑（实心）> 等回答（空心）> 委托中（空心）> 全闲（灭）。
 *
 * 背景一（delegating）：父会话把任务交给子代理后，父 turn 结束（pending 灭）但子仍在跑
 * （pending 亮）—— 只有亮/灭两态时父只能灭，误以为完成。
 *
 * 背景二（awaiting）：AskUserQuestion 把一个逻辑任务切成两轮 —— 提问轮 done 时灯灭，
 * 看着像任务结束，其实 agent 在等用户回答；答完起新轮灯又亮，"完成/运行"反复横跳。
 * turn 语义不动，用空心灯把"在等你回答"表达出来。它与 delegating 同为空心、文案不同：
 * 一个说"子代理在跑"，一个说"轮到你说话了"。
 *
 * 数据来源：delegating 由 pending 真相表 + 后代拓扑推导；awaiting 读会话列表项上的
 * `awaiting_answer`（服务端在 ask_user 未回答时下发）。**刻意不让 awaiting 进 pending
 * 真相表**：那张表喂的是实心灯，塞进去表达的就是相反的意思。
 *
 * fork 不算后代：后端 fork 的 parent_session_key 恒空（manager_test 钉死），
 * 这里再显式排除 source type=fork / fork_from 残留，前后端双保险。
 */

export type SessionPresence = "running" | "awaiting" | "delegating" | "idle";

export type PresenceTreeItem = {
  key?: string;
  session_key?: string;
  parent_session_key?: string;
  source?: string;
  fork_from?: string;
  awaiting_answer?: boolean;
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

function awaitingAnswerOf(item: PresenceTreeItem): boolean {
  return !!(item as any)?.awaiting_answer;
}

export function getSessionPresence(
  selfPending: boolean,
  descendantPending: boolean,
  selfAwaitingAnswer = false,
): SessionPresence {
  if (selfPending) return "running";
  if (selfAwaitingAnswer) return "awaiting";
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
  const awaiting: Record<string, boolean> = {};
  const childrenByParent = new Map<string, string[]>();
  for (const item of items || []) {
    const key = presenceKeyOf(item);
    if (!key) continue;
    // fork 项自己仍要有 presence（它自己的 pending 由自己表达）；只是不算任何人的后代。
    pending[key] = !!(item as any)?.pending;
    awaiting[key] = awaitingAnswerOf(item);
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
    out[key] = getSessionPresence(pending[key], descendantRunning(key), awaiting[key]);
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
  // 本项的「等回答」也从同一份列表里读：调用方拿不到别的通道（列表项就是 awaiting_answer
  // 的载体），而 own 缺失时按 false 处理 —— 列表里没有它，就没有可信的来源。
  const selfAwaiting = awaitingAnswerOf(
    (items || []).find((item) => presenceKeyOf(item) === String(selfKey || "").trim()) || ({} as PresenceTreeItem),
  );
  return getSessionPresence(
    !!selfPending,
    hasRunningDescendant(selfKey, items || [], pendingByKey, (_rootId, sessionKey) => sessionKey, rootId),
    selfAwaiting,
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
