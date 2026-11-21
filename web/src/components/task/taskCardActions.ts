import { isTerminalKanbanTask } from "../../app/appTask";
import type { MessageKey } from "../../i18n";
import type { KanbanTask } from "../../shared/taskTypes";
import type { TaskCardAction } from "./taskBoardTypes";

/** 菜单项的视觉档位；与卡片图标键的颜色语义一致。 */
export type TaskCardActionTone = "default" | "primary" | "warn";

export type TaskCardActionMeta = {
  /** i18n 键：卡片的 title/aria-label 与菜单项的 title/aria-label 同源。 */
  labelKey: MessageKey;
  tone: TaskCardActionTone;
};

export type TaskCardActionKey = Exclude<TaskCardAction, "delete-task">;

/** 动作的展示元数据。删除不在表里：它在卡片与详情两处都单独摆。 */
export const TASK_CARD_ACTION_META: Record<TaskCardActionKey, TaskCardActionMeta> = {
  "finish-worktree": { labelKey: "task.finishWorktree", tone: "primary" },
  pause: { labelKey: "task.actionPause", tone: "default" },
  resume: { labelKey: "task.actionResume", tone: "primary" },
  complete: { labelKey: "task.complete", tone: "primary" },
  cancel: { labelKey: "task.cancelTask", tone: "default" },
  reopen: { labelKey: "task.reopen", tone: "primary" },
  "archive-task": { labelKey: "task.actionArchive", tone: "default" },
  "unarchive-task": { labelKey: "task.actionUnarchive", tone: "primary" },
};

/** 收尾要有可拆的目录：建过 worktree 且目录还在。 */
function canFinishWorktree(task: KanbanTask): boolean {
  return task.create_worktree === true && !!task.worktree_path && task.worktree_missing !== true;
}

/**
 * 一条任务卡片**当前真正可用**的动作，按卡片上从左到右的顺序给出。
 *
 * 看板卡片的按钮行与任务详情 header 的「操作控件」菜单都读这一份 —— 两边各抄
 * 一遍条件，就是「菜单只显示一个、卡片却有好几个」的来源（2026-11-22 实测：
 * 详情菜单只组装了 finish/complete/reopen 三个互斥动作，任何时刻都只剩一个）。
 *
 * 删除**不在**集合里：它是终态任务的破坏性动作，卡片与详情都单独摆在最右。
 */
export function taskCardActions(task: KanbanTask): TaskCardActionKey[] {
  const terminal = isTerminalKanbanTask(task);
  const actions: TaskCardActionKey[] = [];
  if (!terminal) {
    if (canFinishWorktree(task)) actions.push("finish-worktree");
    if (task.status === "running") actions.push("pause");
    if (task.status === "paused") actions.push("resume");
    // 完成只要求「不在跑」：活还在动时先暂停；终态没有后续可完成。
    if (task.status !== "running") actions.push("complete");
    actions.push("cancel");
    return actions;
  }
  // 归档后活已经收起来了：只剩「取消归档」，finish/reopen 一并收起。
  if (task.archived_at) return ["unarchive-task"];
  if (canFinishWorktree(task)) actions.push("finish-worktree");
  actions.push("reopen", "archive-task");
  return actions;
}