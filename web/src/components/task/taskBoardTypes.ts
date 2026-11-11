import type React from "react";
import type { KanbanTask } from "../../shared/taskTypes";

export type KanbanStageColumn = {
  index: number;
  name: string;
  role: "user" | "agent";
  tasks: KanbanTask[];
  groups?: Array<{ key: string; name: string; tone: "success" | "danger" | "muted"; tasks: KanbanTask[] }>;
};

export type TaskSessionErrorDialog = { title: string; message: string; details: string[] };

export type TaskCardAction = "run-now" | "pause" | "resume" | "complete" | "cancel" | "delete-task" | "finish-worktree";

export type MoveKanbanAction = "next" | "run-now" | "pause" | "resume" | "complete" | "cancel" | "delete-task" | "rebuild-worktree" | "finish-worktree";
