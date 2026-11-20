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
 * 返回值按传入顺序保留成员顺序（列表本身按最近活动排序），只保留成员数达到
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
    if (members.length < TASK_GROUP_MIN_SESSIONS) {
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
  | { kind: "group"; taskId: string; label: string }
  // groupLabel：成员名里那段与分组头重复的共享前缀，渲染时剥掉；
  // 同组会话的任务基名是同一次命名调用算出来的，所以直接复用分组头的称呼。
  | { kind: "session"; session: SessionItem; nested: boolean; groupLabel?: string };

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
    out.push({ kind: "group", taskId, label });
    for (const member of members) {
      out.push({ kind: "session", session: member, nested: true, groupLabel: label });
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
 * 分组头文案（2026-11-20 用户决策）：和普通会话同名同字重，末尾补任务号，
 * 不再挂成员计数 —— 组内成员本身就是计数。
 */
export function taskSessionGroupHeading(label: string, taskId: string, fallback: string): string {
  const name = String(label || "").trim() || fallback;
  const id = String(taskId || "").trim();
  return id ? `${name} # ${id}` : name;
}

/**
 * 剥掉组内会话名里与分组头重复的共享前缀（2026-11-20 用户决策：子会话不再
 * 重复显示「任务名 · 」）。
 *
 * 只剥分组头那一段，剩下的部分原样保留：`任务名 · 摘要` → `摘要`，
 * `任务名 / #7 · 摘要` → `#7 · 摘要`（节点号是同组会话之间的区分点，不剥）。
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
    .replace(/^\s*·\s*/, "")
    .trim();
  return rest || name;
}
