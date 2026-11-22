import type React from "react";
import type { KanbanTask } from "../../shared/taskTypes";

/** 看板的一列 —— 列就是**任务状态**（待开始 / 进行中 / 等待你 / 已结束），不是流水阶段。 */
export type KanbanColumn = {
  index: number;
  name: string;
  tasks: KanbanTask[];
  groups?: Array<{ key: string; name: string; tone: "success" | "danger" | "muted"; tasks: KanbanTask[] }>;
};

export type TaskSessionErrorDialog = { title: string; message: string; details: string[] };

export type TaskCardAction =
  | "start"
  | "terminate"
  | "pause"
  | "resume"
  | "complete"
  | "cancel"
  | "reopen"
  | "archive-task"
  | "unarchive-task"
  | "delete-task"
  | "finish-worktree";

export type MoveKanbanAction = TaskCardAction | "rebuild-worktree";