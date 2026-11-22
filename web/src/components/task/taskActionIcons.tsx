import React from "react";
import { ArchiveIcon } from "../session/sessionListShared";
import {
  DeleteIcon,
  TaskCancelIcon,
  TaskCompleteIcon,
  TaskFinishWorktreeIcon,
  TaskPauseIcon,
  TaskReopenIcon,
  TaskResumeIcon,
  TaskStartIcon,
  TaskTerminateIcon,
} from "./taskIcons";
import type { TaskActionButtonKey } from "./taskCardActions";

/**
 * 动作键 → 图标。看板卡片与任务详情 header 读**同一份**。
 *
 * 以前两边各写一张表：卡片这边用「取消」的作废符、详情那边可能挑到别的图形，
 * 或者干脆少一个动作。2026-11-22 用户要求两处样式/颜色/数量/种类/hover 完全统一，
 * 所以连图形也收成一份 —— 键序来自 taskActionButtons，文案与配色来自
 * TASK_ACTION_BUTTON_META，这里只剩图形。
 */
export const taskActionIcons: Record<TaskActionButtonKey, React.ReactNode> = {
  "finish-worktree": <TaskFinishWorktreeIcon />,
  start: <TaskStartIcon />,
  terminate: <TaskTerminateIcon />,
  pause: <TaskPauseIcon />,
  resume: <TaskResumeIcon />,
  complete: <TaskCompleteIcon />,
  cancel: <TaskCancelIcon />,
  reopen: <TaskReopenIcon />,
  "archive-task": <ArchiveIcon />,
  "unarchive-task": <ArchiveIcon />,
  "delete-task": <DeleteIcon />,
};
