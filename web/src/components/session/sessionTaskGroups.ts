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
 * 分组整个消失」）。没有这个字段（旧后端 / 增量广播）时按 seenCount 给的进程内
 * 记忆判，再没有才退回本页成员数。
 *
 * 返回值按传入顺序保留成员顺序（列表本身按最近活动排序），只保留达到
 * TASK_GROUP_MIN_SESSIONS 的任务。
 */
export function collectTaskSessionGroups(
  topLevel: SessionItem[],
  seenCount?: (taskId: string) => number,
): Map<string, SessionItem[]> {
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
      seenCount ? seenCount(taskId) : 0,
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
  // members：组内顶层会话。分组头的指示灯/时间/三点菜单都要按它们聚合，
  // 归档/删除也按它们（后端按 parent_session_key 级联整棵子树）。
  | { kind: "group"; taskId: string; label: string; number: string; members: SessionItem[] }
  // groupLabel：成员名里那段与分组头重复的共享前缀，渲染时剥掉；
  // taskId 让折叠状态能认回自己那一组（成员行本身不带 task_id）。
  | { kind: "session"; session: SessionItem; nested: boolean; groupLabel?: string; taskId?: string };

/**
 * 已见过的任务会话（进程内、只增不减）。
 *
 * 侧栏每次刷新拿到的都是一个窗口：多项目侧栏每根只取 6 条，同一任务的会话一旦
 * 滑出窗口，只按当页成员数判阈值就会让分组凭空消失 —— 用户 2026-11-20 报的
 * 「展开的时候是新样式，收起或者切换到其他节点的时候又变回旧样式」。后端下发的
 * task_session_count 是跨刷新的权威，这里兜住它缺席的路径（旧后端的节点、
 * 增量广播、详情路径），这个标签页见过几条就记几条。
 *
 * 作用域按「节点 + 项目」分开：不同节点/项目里的同名 task_id 不互相充数。
 * 只在内存里，页面重新加载后从零开始（那时后端计数已经在）。
 */
const seenTaskSessions = new Map<string, Set<string>>();

const TALLY_SEPARATOR = "\u0000";

export function rememberTaskSessions(scope: string, items: readonly SessionItem[]): void {
  for (const item of items) {
    const taskId = String(item.task_id || "").trim();
    const key = String(item.key || item.session_key || "");
    if (!taskId || !key) continue;
    // 只认顶层会话：子会话本来就不参与分组，记进来会把阈值虚高。
    if (String(item.parent_session_key || "").trim()) continue;
    const tallyKey = `${scope}${TALLY_SEPARATOR}${taskId}`;
    const seen = seenTaskSessions.get(tallyKey);
    if (seen) {
      seen.add(key);
      continue;
    }
    seenTaskSessions.set(tallyKey, new Set([key]));
  }
}

export function taskSessionSeenCount(scope: string, taskId: string): number {
  return seenTaskSessions.get(`${scope}${TALLY_SEPARATOR}${taskId}`)?.size || 0;
}

/** 用例之间必须能清干净：这份记忆是模块级的，会跨用例累积。 */
export function resetTaskSessionTally(): void {
  seenTaskSessions.clear();
}

export function orderTaskGroupedRows(
  topLevel: SessionItem[],
  seenCount?: (taskId: string) => number,
): TaskGroupedEntry[] {
  const groups = collectTaskSessionGroups(topLevel, seenCount);
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
    out.push({ kind: "group", taskId, label, number: taskSessionGroupNumber(members), members });
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
 * 任务分组的聚合状态（2026-11-20 用户决策：分组头也要有指示灯、时间和
 * 问题/报错标识，「按照普通会话显示」）。
 *
 * 普通会话行只有一个 `presence`/`pending`，分组身后是一批会话，所以这里做聚合：
 *   - running：任一成员在跑（自己回复中 pending、派生态 running/delegating）；
 *   - awaiting：任一成员停着等你回答；
 *   - askingUser / errors：挂 aux 问号、带 session_error 的成员条数
 *     （用户要「有问题和报错则传递出来」，所以报条数而不是布尔）；
 *   - latestAt：成员里最晚的活动时间（closed_at 优先、否则 updated_at，
 *     与会话行取时间同一口径）——「时间按任务组内的最新时间更新」；
 *   - archived：成员是否都已归档（归档面板里菜单要变成「取消归档」）。
 *
 * 刻意不聚合 plan/todo/task 这类内容徽标：它们描述单条会话的内容，堆到分组头
 * 上只会挤掉名字，用户这一轮也只要求传递「问题」和「报错」。
 */
export type TaskGroupAggregate = {
  running: boolean;
  awaiting: boolean;
  askingUser: number;
  errors: number;
  latestAt: string;
  archived: boolean;
  pinned: boolean;
};

export function taskGroupAggregateState(members: readonly SessionItem[]): TaskGroupAggregate {
  let running = false;
  let awaiting = false;
  let askingUser = 0;
  let errors = 0;
  let latestAt = "";
  let latestMs = -Infinity;
  let archived = members.length > 0;
  // 置顶是按会话存的（services/pins 的 root::key 表），分组没有自己的条目。
  // 整组口径取「成员全部置顶」：置顶动作一次把全部成员写上，取消一次全部清掉，
  // 所以正常不会停在半置顶；真停在混合态时按「未置顶」显示，再点一下是把剩下的补齐。
  let pinned = members.length > 0;
  for (const member of members) {
    if (member.pending || member.presence === "running" || member.presence === "delegating") {
      running = true;
    }
    if (member.presence === "awaiting") {
      awaiting = true;
    }
    if (member.aux_flags?.ask_user_waiting) {
      askingUser += 1;
    }
    if (String(member.aux_flags?.session_error || "").trim()) {
      errors += 1;
    }
    if (!member.archived_at) {
      archived = false;
    }
    if (!String(member.pinned_at || "").trim()) {
      pinned = false;
    }
    const raw = String(member.closed_at || member.updated_at || "");
    const ms = Date.parse(raw);
    if (Number.isFinite(ms) && ms > latestMs) {
      latestMs = ms;
      latestAt = raw;
    }
  }
  return { running, awaiting, askingUser, errors, latestAt, archived, pinned };
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
 *
 * 唯一的例外是「名字里只剩任务号」：`任务名 / #87`（同一任务的多条会话都没带
 * 摘要）剥完是空串，回落到完整名字就等于一点没剥 —— 用户 2026-11-20 第三次
 * 反馈「『任务详情面板修改 / #87』的子会话还是完整的名字」。这时留 `#87`：
 * 任务号是组内唯一的区分点，也比整行重复一遍任务名有用。
 */
export function taskSessionMemberLabel(fullName: string, groupLabel: string): string {
  const name = String(fullName || "").trim();
  const label = String(groupLabel || "").trim();
  if (!name || !label || !name.startsWith(label)) return name;
  const tail = name.slice(label.length);
  // 前缀必须在词边界上：`任务 A` 不能把 `任务 AB · 摘要` 切成 `B · 摘要`。
  if (tail && !/^[\s·/]/.test(tail)) return name;
  const number = /#(\d+)/.exec(tail);
  const rest = tail
    .replace(/^\s*\/\s*/, "")
    .replace(/^#\d+\s*·?\s*/, "")
    .replace(/^\s*·\s*/, "")
    .trim();
  if (rest) return rest;
  if (number) return `#${number[1]}`;
  return name;
}
