// 对话流的纯数据逻辑：WS 增量合并、发送守卫、状态文案选择。
//
// 放在 shared 而不是 hook 里：这些函数没有副作用，组件与 hook 共用，也便于
// 在 node 环境直接测试（仓库没有 jsdom / testing-library）。
import type { ActivityStatus, TaskActivity, WorkflowChoice } from "./taskComments";

const VALID_STATUSES: ReadonlySet<string> = new Set([
  "pending",
  "running",
  "completed",
  "failed",
  "cancelled",
  "waiting_user",
]);

/** 后端缺省/异常的状态一律按 pending 展示，避免出现空徽标。 */
export function normalizeActivityStatus(status: unknown): ActivityStatus {
  const value = typeof status === "string" ? status.trim() : "";
  return (VALID_STATUSES.has(value) ? value : "pending") as ActivityStatus;
}

/**
 * 按 id 合并单条活动。
 *
 * 同 id 已存在时覆盖（WS 回声与 HTTP 响应可能先后到达）；不存在则追加到末尾，
 * 保持服务端返回的时序。返回新数组以便 React 感知变化。
 */
export function applyActivityCreated(
  activities: TaskActivity[],
  incoming: TaskActivity | null | undefined,
): TaskActivity[] {
  if (!incoming || typeof incoming.id !== "string" || incoming.id === "") {
    return activities;
  }
  const index = activities.findIndex((activity) => activity.id === incoming.id);
  if (index < 0) return [...activities, incoming];
  const next = activities.slice();
  next[index] = { ...activities[index], ...incoming };
  return next;
}

/**
 * 应用 `task.activity.updated` 的 changes。
 *
 * 找不到目标 id 时原样返回同一个数组 —— 调用方据此可以避免无谓重渲染，
 * 也防止「重连后才收到的旧更新」凭空插入一条活动。
 */
export function applyActivityUpdated(
  activities: TaskActivity[],
  activityId: string | null | undefined,
  changes: Record<string, unknown> | null | undefined,
): TaskActivity[] {
  if (!activityId || !changes) return activities;
  let matched = false;
  const next: TaskActivity[] = [];
  for (const activity of activities) {
    if (activity.id !== activityId) {
      next.push(activity);
      continue;
    }
    matched = true;
    // Router 的 silent / deliver_to_active_run 会把占位回复标成 hidden：
    // 行还在服务端（审计与幂等用），但用户可见活动流里必须移除。
    if (changes.hidden === true) continue;
    next.push({ ...activity, ...changes } as TaskActivity);
  }
  return matched ? next : activities;
}

export type CanSendActivityInput = {
  body: string;
  workflowChoice: WorkflowChoice;
  workflowVersionId?: string;
  sending?: boolean;
};

/**
 * 发送守卫（Review Focus #1 的前端半边）。
 *
 * 空留言、指定的 workflow 却没选版本、或在途时都不能发；后端仍有 400 兜底。
 */
export function canSendActivity(input: CanSendActivityInput): boolean {
  if (input.sending) return false;
  if (!input.body.trim()) return false;
  if (input.workflowChoice === "specified" && !String(input.workflowVersionId || "").trim()) {
    return false;
  }
  return true;
}

/**
 * 卡片正文的选择规则：
 * - 结束/停等状态优先展示 summary（等待你处理时 summary 带原因）；
 * - pending/running 展示 body（运行中后端会把「步骤 i/n」写进 body）。
 */
export function activityDisplayText(activity: TaskActivity): string {
  const status = normalizeActivityStatus(activity.status);
  const body = (activity.body || "").trim();
  const summary = (activity.summary || "").trim();
  if (
    status === "completed" ||
    status === "failed" ||
    status === "cancelled" ||
    status === "waiting_user"
  ) {
    return summary || body;
  }
  return body || summary;
}

/** 是否属于当前任务（WS 广播是全局的，必须按 task_id 过滤）。 */
export function activityBelongsToTask(
  payload: { task_id?: unknown; root_id?: unknown } | null | undefined,
  taskId: string,
  rootId?: string,
): boolean {
  if (!payload || payload.task_id !== taskId) return false;
  if (rootId && typeof payload.root_id === "string" && payload.root_id !== "" && payload.root_id !== rootId) {
    return false;
  }
  return true;
}

export type TaskActivityPayload = Record<string, unknown>;

/**
 * question / human_gate 卡的结构化内容。payload_json 是后端原样透传的字符串；
 * 非法 JSON 或非对象一律当空对象 —— 一张坏卡不能把整段对话打崩。
 */
export function parseTaskActivityPayload(
  activity: Pick<TaskActivity, "payload_json"> | null | undefined,
): TaskActivityPayload {
  const raw = activity?.payload_json;
  if (typeof raw !== "string" || raw.trim() === "") return {};
  try {
    const parsed: unknown = JSON.parse(raw);
    return parsed && typeof parsed === "object" && !Array.isArray(parsed)
      ? (parsed as TaskActivityPayload)
      : {};
  } catch {
    return {};
  }
}

/** 仍是「等人回答/审批」的卡：只有 waiting_user 的 question / human_gate 算。 */
export function isPendingTaskPrompt(activity: TaskActivity | null | undefined): boolean {
  if (!activity) return false;
  if (activity.type !== "question" && activity.type !== "human_gate") return false;
  return normalizeActivityStatus(activity.status) === "waiting_user";
}

/**
 * 任务对话当前挡在输入前面的那张卡。
 *
 * 返回最早的一条等待卡：同一时刻通常只有一张，但历史数据里可能残留多张
 * （例如先有未回答的问题，后又出现 gate）。按数组顺序取第一条，保证 UI
 * 的阻塞对象稳定，不会随重渲染跳动。
 */
export function pendingTaskPrompt(activities: TaskActivity[]): TaskActivity | null {
  for (const activity of activities) {
    if (isPendingTaskPrompt(activity)) return activity;
  }
  return null;
}

/** 结算行：带 summary 的终态 agent 回复。折叠只在它落地后才允许。 */
function isSettledReply(activity: TaskActivity): boolean {
  if (activity.type !== "agent_reply" || activity.role !== "agent") return false;
  const status = normalizeActivityStatus(activity.status);
  if (status !== "completed" && status !== "failed" && status !== "waiting_user") return false;
  return (activity.summary || "").trim() !== "";
}

/**
 * 对话流的行序列：普通活动一行，折叠后的运行汇报一组。
 *
 * 为什么需要折叠：结算回复（保证路径）与运行期 `purpose=progress` 汇报
 * （尽力路径）都写结果句。T10 裁定两条数据都不能删（结算回填保证终态可见，
 * 汇报带失败节点/未验证项等增量信息），所以只能在展示层去重：同一留言下
 * 第一个已结算的回复留在原位，其余 progress 行折成它后面的一组。
 *
 * 不折叠的三种情况，都是「折叠会藏掉用户正要的信息」：
 * - 父行还没结算（运行中用户就靠这行知道流程起来了）；
 * - progress 行没有 reply_to_id，无法确定归属；
 * - 同一留言下没有结算回复（全是 progress，那它们就是唯一的信息）。
 */
export type ConversationRow =
  | { kind: "activity"; activity: TaskActivity }
  | { kind: "reports"; parentId: string; reports: TaskActivity[] };

export function conversationRows(activities: TaskActivity[]): ConversationRow[] {
  const settledByMessage = new Map<string, string>();
  for (const activity of activities) {
    const messageId = (activity.reply_to_id || "").trim();
    if (messageId === "" || settledByMessage.has(messageId)) continue;
    if (isSettledReply(activity)) settledByMessage.set(messageId, activity.id);
  }
  // 折叠目标：父行 = 同一留言下第一个已结算的回复；父行自己与 purpose 缺省的
  // 历史行都不折。先算一遍归属，输出时再按父行位置插组 —— 组必须紧跟它所属的
  // 那一轮，不能因为汇报到达得晚就掉到下一轮留言后面。
  const parentOf = (activity: TaskActivity): string => {
    const messageId = (activity.reply_to_id || "").trim();
    const parentId = messageId ? settledByMessage.get(messageId) : undefined;
    if (!parentId || parentId === activity.id || activity.purpose !== "progress") return "";
    return parentId;
  };
  const reportsByParent = new Map<string, TaskActivity[]>();
  for (const activity of activities) {
    const parentId = parentOf(activity);
    if (parentId === "") continue;
    const reports = reportsByParent.get(parentId);
    if (reports) reports.push(activity);
    else reportsByParent.set(parentId, [activity]);
  }

  const rows: ConversationRow[] = [];
  for (const activity of activities) {
    if (parentOf(activity) !== "") continue;
    rows.push({ kind: "activity", activity });
    const reports = reportsByParent.get(activity.id);
    if (reports) rows.push({ kind: "reports", parentId: activity.id, reports });
  }
  return rows;
}
