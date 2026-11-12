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
  const next = activities.map((activity) => {
    if (activity.id !== activityId) return activity;
    matched = true;
    return { ...activity, ...changes } as TaskActivity;
  });
  return matched ? next : activities;
}

export type CanSendActivityInput = {
  body: string;
  workflowChoice: WorkflowChoice;
  workflowVersionId?: string;
  sending?: boolean;
  disabled?: boolean;
};

/**
 * 发送守卫（Review Focus #1 的前端半边）。
 *
 * 空留言、指定的 workflow 却没选版本、或在途/禁用时都不能发；后端仍有 400 兜底。
 */
export function canSendActivity(input: CanSendActivityInput): boolean {
  if (input.disabled || input.sending) return false;
  if (!input.body.trim()) return false;
  if (input.workflowChoice === "specified" && !String(input.workflowVersionId || "").trim()) {
    return false;
  }
  return true;
}

/**
 * 卡片正文的选择规则：
 * - 终态（completed/failed/cancelled）优先展示 summary，没有时回落 body；
 * - pending/running 展示 body（运行中后端会把「步骤 i/n」写进 body）。
 */
export function activityDisplayText(activity: TaskActivity): string {
  const status = normalizeActivityStatus(activity.status);
  const body = (activity.body || "").trim();
  const summary = (activity.summary || "").trim();
  if (status === "completed" || status === "failed" || status === "cancelled") {
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