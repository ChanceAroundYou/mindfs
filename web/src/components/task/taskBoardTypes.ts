import type React from "react";
import type { KanbanTask, TaskTemplate } from "../../shared/taskTypes";

export type KanbanStageColumn = {
  index: number;
  name: string;
  role: "user" | "agent";
  tasks: KanbanTask[];
  groups?: Array<{ key: string; name: string; tone: "success" | "danger" | "muted"; tasks: KanbanTask[] }>;
};

export type TaskSessionErrorDialog = { title: string; message: string; details: string[] };

export type MoveKanbanAction = "next" | "run-now" | "pause" | "resume" | "complete" | "cancel" | "delete-task" | "rebuild-worktree" | "finish-worktree";

export type TaskTemplateControls = {
  taskTemplateActionMenuRef: React.RefObject<HTMLDivElement | null>;
  taskTemplateActionMenuOpen: boolean;
  setTaskTemplateActionMenuOpen: React.Dispatch<React.SetStateAction<boolean>>;
  taskCreateTemplateMenuRef: React.RefObject<HTMLDivElement | null>;
  taskCreateTemplateMenuOpen: boolean;
  setTaskCreateTemplateMenuOpen: React.Dispatch<React.SetStateAction<boolean>>;
  taskTemplates: TaskTemplate[];
  taskTemplateFilter: string;
  setTaskTemplateFilter: React.Dispatch<React.SetStateAction<string>>;
  openTaskTemplateEditor: (template: TaskTemplate | null) => void;
  handleDeleteTaskTemplate: (template: TaskTemplate) => Promise<void>;
};
