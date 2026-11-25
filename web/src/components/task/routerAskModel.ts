import type { KanbanTask } from "../../shared/taskTypes";

/**
 * 任务卡上的 Router 提问角标。
 *
 * Router 自己派发的 ask 落在 `router_ask`（服务端只认 `session_key` 为空的
 * `waiting_user` 提问卡）；会话 worker 透传的 ask 仍走 `aux_flags.ask_user_waiting`。
 * 两个标记分源，不会互相置位，也不会在卡片上合并成同一个徽标。
 */
export type TaskRouterAskBadge = {
  key: "router_ask";
  count: number;
  question: string;
  attention: boolean;
  since: string;
};

export function taskRouterAskBadge(
  task: Pick<KanbanTask, "router_ask"> | null | undefined,
): TaskRouterAskBadge | null {
  if (!task) return null;
  // 写成 task.router_ask（而不是 task?.router_ask），让卡片字段守卫能从源码里
  // 抽出这个读取；否则投影漏字段时它抓不到。
  const ask = task.router_ask;
  if (!ask) return null;
  return {
    key: "router_ask",
    count: Math.max(1, Number(ask.count) || 1),
    question: typeof ask.question === "string" ? ask.question.trim() : "",
    attention: true,
    since: typeof ask.since === "string" ? ask.since : "",
  };
}
