import type { KanbanTask } from "../../shared/taskTypes";

/**
 * 任务卡上的 Router 角标（W3-4）。
 *
 * 「什么算未处理」由服务端决定（`task.RouterAlert` 只在事件账本里那条健康事件
 * 还没被 Router 消费时下发，见 server/internal/kanban/task_router_alert.go）。
 * 这里只做展示映射：档位 → 文案键、条数下限、是否脉冲。浏览器不重算语义，
 * 所以「处理完就消失」是服务端事实，不是前端计时器。
 */
export type TaskRouterAlertBadge = {
  key: "router_interrupted" | "router_stalled";
  labelKey: "task.routerAlertInterrupted" | "task.routerAlertStalled";
  count: number;
  attention: boolean;
  since: string;
};

export function taskRouterAlertBadge(task: Pick<KanbanTask, "router_alert"> | null | undefined): TaskRouterAlertBadge | null {
  const alert = task?.router_alert;
  if (!alert) return null;
  const count = Math.max(1, Number(alert.count) || 1);
  const since = typeof alert.since === "string" ? alert.since : "";
  if (alert.kind === "turn_interrupted") {
    return { key: "router_interrupted", labelKey: "task.routerAlertInterrupted", count, attention: true, since };
  }
  if (alert.kind === "turn_stalled") {
    return { key: "router_stalled", labelKey: "task.routerAlertStalled", count, attention: true, since };
  }
  // 未知档位不显示：后端将来加档要先在两端同时定义文案，不能拿旧文案硬套。
  return null;
}
