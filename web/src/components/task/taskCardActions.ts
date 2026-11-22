import { isTerminalKanbanTask } from "../../app/appTask";
import type { MessageKey } from "../../i18n";
import type { KanbanTask } from "../../shared/taskTypes";
import type { TaskCardAction } from "./taskBoardTypes";

/** 图标键的视觉档位；卡片与详情两处渲染共用同一份映射。 */
export type TaskCardActionTone = "default" | "primary" | "success" | "warn";

export type TaskCardActionMeta = {
  /** i18n 键：卡片的 title/aria-label 与菜单项的 title/aria-label 同源。 */
  labelKey: MessageKey;
  tone: TaskCardActionTone;
};

export type TaskCardActionKey = Exclude<TaskCardAction, "delete-task">;

/** 图标键的完整集合：卡片与详情渲染的按钮都从这里取（删除也在内）。 */
export type TaskActionButtonKey = TaskCardActionKey | "delete-task";

/** 动作的展示元数据。卡片与详情读同一份：文案、配色不再各写一套。 */
export const TASK_ACTION_BUTTON_META: Record<TaskActionButtonKey, TaskCardActionMeta> = {
  "finish-worktree": { labelKey: "task.finishWorktree", tone: "success" },
  pause: { labelKey: "task.actionPause", tone: "default" },
  resume: { labelKey: "task.actionResume", tone: "primary" },
  complete: { labelKey: "task.complete", tone: "success" },
  cancel: { labelKey: "task.cancelTask", tone: "default" },
  reopen: { labelKey: "task.reopen", tone: "primary" },
  "archive-task": { labelKey: "task.actionArchive", tone: "default" },
  "unarchive-task": { labelKey: "task.actionUnarchive", tone: "primary" },
  "delete-task": { labelKey: "task.deleteTask", tone: "warn" },
};

/** 收尾要有可拆的目录：建过 worktree 且目录还在。 */
function canFinishWorktree(task: KanbanTask): boolean {
  return task.create_worktree === true && !!task.worktree_path && task.worktree_missing !== true;
}

/**
 * 一条任务卡片**当前真正可用**的动作，按卡片上从左到右的顺序给出。
 *
 * 这是「哪些动作此刻存在」的唯一口径；`taskActionButtons` 在这份基础上补上删除，
 * 卡片与详情 header 的图标行都直接按它渲染（2026-11-22 用户要求两处完全统一）。
 *
 * 删除**不在**集合里：它只给终态任务，由 `taskActionButtons` 追加在最右。
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
  // 归档：能拿回活动列（返回 / 取消归档），要么真删掉。finish 收起来。
  if (task.archived_at) return ["reopen", "unarchive-task"];
  if (canFinishWorktree(task)) actions.push("finish-worktree");
  actions.push("reopen", "archive-task");
  return actions;
}

/**
 * 卡片与详情**实际渲染**的那一排图标键（含删除）。
 *
 * 两处都只读这一份，连顺序也一致 —— 各抄一遍条件就是「卡片有、详情没有」的来源。
 */
export function taskActionButtons(task: KanbanTask): TaskActionButtonKey[] {
  const keys: TaskActionButtonKey[] = taskCardActions(task);
  if (isTerminalKanbanTask(task)) keys.push("delete-task");
  return keys;
}