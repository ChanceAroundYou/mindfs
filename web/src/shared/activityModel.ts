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
  /** 带待上传附件时允许空正文（与会话输入框一致：只发一张图也能发）。 */
  hasAttachments?: boolean;
};

/**
 * 发送守卫（Review Focus #1 的前端半边）。
 *
 * 空留言、指定的 workflow 却没选版本、或在途时都不能发；后端仍有 400 兜底。
 */
export function canSendActivity(input: CanSendActivityInput): boolean {
  if (input.sending) return false;
  if (!input.body.trim() && !input.hasAttachments) return false;
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

/**
 * 运行汇报行：`purpose=progress` 的 Agent 回复。
 *
 * 这类行不再折叠（2026-11-19 用户裁定），但需要一个可见标记，让用户能分清
 * 「这一轮的结算结果」与「运行过程中 Router 的上报」。
 */
export function isRunReport(activity: TaskActivity): boolean {
  return activity.type === "agent_reply" && activity.role === "agent" && activity.purpose === "progress";
}

/**
 * 对话流实际要渲染的活动。
 *
 * 唯一的过滤：同一 Run 正停在审批卡上时，隐藏那条等待中的占位回复。
 * 结算会把 gate 的 prompt 写进占位回复的 summary，于是 #88 现场出现两张
 * 冲突的卡：一张「验收 / 等待你处理」的回复，一张真正的审批卡。用户分不清
 * 该点哪个，也读不出「审批已经完成」的错觉。占位回复不是结果，审批卡才是
 * 唯一的处理入口；gate 结算后（success/cancelled）占位回复照常回到结果行。
 *
 * 没有审批卡的 waiting_user 回复（条件求值失败等）不过滤：那行就是唯一说明。
 */
export function visibleConversationActivities(activities: TaskActivity[]): TaskActivity[] {
  const waitingRuns = new Set<string>();
  for (const activity of activities) {
    if (activity.type === "human_gate" && normalizeActivityStatus(activity.status) === "waiting_user" && activity.run_id) {
      waitingRuns.add(activity.run_id);
    }
  }
  if (waitingRuns.size === 0) return activities;
  return activities.filter((activity) => {
    if (activity.type !== "agent_reply" || activity.role !== "agent") return true;
    if (normalizeActivityStatus(activity.status) !== "waiting_user") return true;
    return !(activity.run_id && waitingRuns.has(activity.run_id));
  });
}
