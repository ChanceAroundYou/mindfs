import type { SessionItem } from "./sessionListTypes";

/** 一个任务至少要有这么多条顶层会话才值得分组 —— 单条会话套一层分组头只是多一行噪音。 */
export const TASK_GROUP_MIN_SESSIONS = 2;

/**
 * 把同一个 task_id 下的顶层会话收成一组（2026-11-20 用户决策：UI 层按 task_id
 * 分组，零数据改动）。
 *
 * 只收顶层会话：真正的子会话（parent_session_key 指向列表里存在的会话）已经挂在
 * 父会话下面，再按任务分一次组会出现「组里套组」。fork 会话在调用方就已经是顶层，
 * 它们带 task_id 时照常入组。
 *
 * 阈值看「这个任务一共有几条顶层会话」而不是「这一页装了几条」：后端列表查询
 * 下发 task_session_count，多项目侧栏每根只取 6 条时，同一任务的会话常常只有
 * 一条落在窗口里 —— 按装了几条判会让分组时有时无（用户 2026-11-20 报「刷新后
 * 分组整个消失」）。没有这个字段（旧后端）时退回按本页成员数判。
 *
 * 返回值按传入顺序保留成员顺序（列表本身按最近活动排序），只保留达到
 * TASK_GROUP_MIN_SESSIONS 的任务。
 */
export function collectTaskSessionGroups(topLevel: SessionItem[]): Map<string, SessionItem[]> {
  const groups = new Map<string, SessionItem[]>();
  for (const item of topLevel) {
    const taskId = String(item.task_id || "").trim();
    if (!taskId) continue;
    const members = groups.get(taskId);
    if (members) {
      members.push(item);
      continue;
    }
    groups.set(taskId, [item]);
  }
  for (const [taskId, members] of groups) {
    const total = Math.max(
      members.length,
      ...members.map((item) => Number(item.task_session_count) || 0),
    );
    if (total < TASK_GROUP_MIN_SESSIONS) {
      groups.delete(taskId);
    }
  }
  return groups;
}

/**
 * 把顶层会话排成「分组头 + 成员」的行序列：只有成员数达到
 * TASK_GROUP_MIN_SESSIONS 的任务才成组，组出现在它第一个成员原来的位置上，
 * 其余成员紧随其后（列表本身按最近活动排序，组内相对顺序保持不变）。
 * 没有 task_id 或只有一条会话的行原样保留在原位置。
 */
export type TaskGroupedEntry =
  | { kind: "group"; taskId: string; label: string; number: string }
  // groupLabel：成员名里那段与分组头重复的共享前缀，渲染时剥掉；
  // taskId 让折叠状态能认回自己那一组（成员行本身不带 task_id）。
  | { kind: "session"; session: SessionItem; nested: boolean; groupLabel?: string; taskId?: string };

export function orderTaskGroupedRows(topLevel: SessionItem[]): TaskGroupedEntry[] {
  const groups = collectTaskSessionGroups(topLevel);
  const emitted = new Set<string>();
  const out: TaskGroupedEntry[] = [];
  for (const item of topLevel) {
    const taskId = String(item.task_id || "").trim();
    const members = taskId ? groups.get(taskId) : undefined;
    if (!members) {
      out.push({ kind: "session", session: item, nested: false });
      continue;
    }
    if (emitted.has(taskId)) continue;
    emitted.add(taskId);
    const label = taskSessionGroupLabel(members);
    out.push({ kind: "group", taskId, label, number: taskSessionGroupNumber(members) });
    for (const member of members) {
      out.push({ kind: "session", session: member, nested: true, groupLabel: label, taskId });
    }
  }
  return out;
}

/**
 * 分组头显示的任务称呼。会话名的两种来源都要能剥回任务本身：
 *   - 一轮 Run 的共享会话：「任务名 · 第一条留言摘要」
 *   - 显式 per_node / 旧数据的节点会话：「任务名 / #7 · 第一条留言摘要」
 * 取第一条有名字的会话即可 —— 同组会话的任务基名是同一次调用算出来的，必然一致。
 */
export function taskSessionGroupLabel(sessions: SessionItem[]): string {
  for (const item of sessions) {
    const name = String(item.name || "").trim();
    if (!name) continue;
    const base = name.split(" · ")[0].trim();
    const bare = base.replace(/\s*\/\s*#\d+$/, "").trim();
    if (bare) return bare;
  }
  return "";
}

/**
 * 分组头的任务号。`task_id` 是 `task_270967ce0da8157` 这种不透明串，直接显示只是
 * 一串哈希；人看的任务号在会话名里 —— 节点会话名是 `任务名 / #91 · …`，
 * `#91` 就是看板上的任务号（后端 `TaskSessionName(name, taskNumber)` 拼的）。
 * 一轮 Run 的共享会话名（`任务名 · 摘要`）里没有任务号，这时宁可不显示，也不
 * 拿 `task_id` 充数。
 */
export function taskSessionGroupNumber(sessions: SessionItem[]): string {
  for (const item of sessions) {
    const match = String(item.name || "").match(/\/\s*#(\d+)/);
    if (match) return match[1];
  }
  return "";
}

/**
 * 分组头文案（2026-11-20 用户决策）：和普通会话同名同字重，末尾补任务号，
 * 不再挂成员计数 —— 组内成员本身就是计数。
 */
export function taskSessionGroupHeading(label: string, number: string, fallback: string): string {
  const name = String(label || "").trim() || fallback;
  const id = String(number || "").trim();
  return id ? `${name} # ${id}` : name;
}

/**
 * 分组这一行展开不展开（2026-11-20 用户决策：默认收起，点分组头展开，
 * 和子会话的展开一个逻辑）。选中的会话在组内时强制展开 —— 否则用户点进
 * 一条会话、它自己却从列表里消失，选区就丢了。
 */
export function taskGroupShowsMembers(
  expanded: Record<string, boolean>,
  taskId: string,
  members: SessionItem[],
  selectedKey: string,
): boolean {
  if (selectedKey && members.some((member) => member.key === selectedKey)) return true;
  return !!expanded[taskId];
}

/**
 * 剥掉组内会话名里与分组头重复的共享前缀（2026-11-20 用户决策：子会话不再
 * 重复显示「任务名 · 」）。
 *
 * 分组头那一段连任务号一起剥：`任务名 · 摘要` → `摘要`，
 * `任务名 / #91 · 节点名 · 摘要` → `节点名 · 摘要`。任务号已经在分组头上，
 * 成员行再挂一次只是噪音（用户 2026-11-20 第二次反馈）。
 * 用户改过名的会话不再以任务基名开头，这时原样返回，绝不猜着切。
 */
export function taskSessionMemberLabel(fullName: string, groupLabel: string): string {
  const name = String(fullName || "").trim();
  const label = String(groupLabel || "").trim();
  if (!name || !label || !name.startsWith(label)) return name;
  const tail = name.slice(label.length);
  // 前缀必须在词边界上：`任务 A` 不能把 `任务 AB · 摘要` 切成 `B · 摘要`。
  if (tail && !/^[\s·/]/.test(tail)) return name;
  const rest = tail
    .replace(/^\s*\/\s*/, "")
    .replace(/^#\d+\s*·?\s*/, "")
    .replace(/^\s*·\s*/, "")
    .trim();
  return rest || name;
}
