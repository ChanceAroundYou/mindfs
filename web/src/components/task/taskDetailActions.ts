import { isTerminalKanbanTask } from "../../app/appTask";
import type { MessageKey } from "../../i18n";
import type { KanbanTask } from "../../shared/taskTypes";

/**
 * 任务详情 header 平铺按钮的动作表。
 *
 * 与看板卡片的 `taskCardActions` **故意不同**：详情面板按用户 2026-11-22 的口径
 * 只给「这一刻最该做的事」，卡片仍保留完整的动作行（暂停/恢复/取消/收尾…）。
 * 已知差异（改这里前先读 `docs/upstream-customizations.yaml` 的 G-NY 边界）：
 *   - 已归档也给「返回」：后端 `ReopenTask` 本来就会清 `archived_at`，能直接回活动列；
 *   - 待开始给「开始」：走 `RunNow` 派发排队中的首条留言，卡片上仍是蓝点/发消息；
 *   - 执行中只给「中断」：对应后端的 pause，不再同时摆「取消」；
 *   - 已暂停未在用户清单里，给「恢复」，否则中断后无处可去；
 *   - 卡片上的「收尾 worktree」不在详情里，worktree 清场仍从卡片发起。
 */
export type TaskDetailActionTone = "default" | "primary" | "warn";

export type TaskDetailActionMeta = {
  labelKey: MessageKey;
  tone: TaskDetailActionTone;
};

export type TaskDetailActionKey =
  | "start"
  | "interrupt"
  | "resume"
  | "complete"
  | "cancel"
  | "reopen"
  | "archive-task"
  | "unarchive-task"
  | "delete-task";

export const TASK_DETAIL_ACTION_META: Record<TaskDetailActionKey, TaskDetailActionMeta> = {
  start: { labelKey: "task.actionStart", tone: "primary" },
  interrupt: { labelKey: "task.actionInterrupt", tone: "default" },
  resume: { labelKey: "task.actionResume", tone: "primary" },
  complete: { labelKey: "task.complete", tone: "primary" },
  cancel: { labelKey: "task.cancelTask", tone: "default" },
  reopen: { labelKey: "task.reopen", tone: "primary" },
  "archive-task": { labelKey: "task.actionArchive", tone: "default" },
  "unarchive-task": { labelKey: "task.actionUnarchive", tone: "primary" },
  "delete-task": { labelKey: "task.deleteTask", tone: "warn" },
};

export function taskDetailActions(task: KanbanTask): TaskDetailActionKey[] {
  // 归档卡：先给「返回」（ReopenTask 会清 archived_at），再给取消归档与删除。
  if (task.archived_at) return ["reopen", "unarchive-task", "delete-task"];
  // 完成 / 取消（含失败）：只留回退、归档与删除。
  if (isTerminalKanbanTask(task)) return ["reopen", "archive-task", "delete-task"];
  if (task.status === "running") return ["interrupt"];
  if (task.status === "paused") return ["resume"];
  if (task.status === "pending") return ["start"];
  // 待审阅（waiting_user）以及任何活动态兜底：完成或取消。
  return ["complete", "cancel"];
}
