import React from "react";
import { useI18n } from "../../i18n";
import { isTerminalKanbanTask, taskStatusLabel } from "../../app/appTask";
import {
  TaskCompleteIcon,
  TaskPauseIcon,
  TaskResumeIcon,
  TaskRunNowIcon,
  taskCardIconButtonStyle,
} from "../../app/taskIcons";
import type { WorkspaceTaskItem } from "../../app/useWorkspaceBoard";
import {
  workspaceTaskMetaStyle,
  workspaceTaskNameStyle,
  workspaceTaskNumberStyle,
  workspaceTaskRowHoverStyle,
  workspaceTaskRowStyle,
} from "./workspaceStyles";

/**
 * 单条任务行：紧凑单行，不是看板那种大卡。
 *
 * 刻意不复制看板卡片：工作台只回答「各项目在干什么」，要改名/改段就去项目看板。
 * 行内只留三个就地操作 —— 完成、立刻跑、暂停/继续，都是不改任务内容的状态迁移。
 */
export function WorkspaceTaskRow({ item, onOpen, onComplete, onRunNow, onTogglePause }: {
  item: WorkspaceTaskItem;
  onOpen: (item: WorkspaceTaskItem) => void;
  onComplete: (item: WorkspaceTaskItem) => void;
  onRunNow: (item: WorkspaceTaskItem) => void;
  onTogglePause: (item: WorkspaceTaskItem) => void;
}) {
  const { t } = useI18n();
  const task = item.task;
  const stopped = isTerminalKanbanTask(task);
  const name = task.name || item.root_name || task.task_template_name || t("task.unnamedTemplate");
  return (
    <div
      role="button"
      tabIndex={0}
      onClick={() => onOpen(item)}
      onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); onOpen(item); } }}
      style={{ ...workspaceTaskRowStyle, ...workspaceTaskRowHoverStyle }}
    >
      <span style={workspaceTaskNumberStyle}>{task.task_number ? `#${task.task_number}` : ""}</span>
      <span style={workspaceTaskNameStyle}>{name}</span>
      <span style={workspaceTaskMetaStyle}>{taskStatusLabel(task.status, t)}</span>
      {/* 就地操作：完成不依赖会话/worktree，非终态都给；暂停/继续只给在跑的 */}
      {!stopped ? (
        <button
          type="button"
          title={t("task.completeShort")}
          aria-label={t("task.completeShort")}
          onClick={(event) => { event.stopPropagation(); onComplete(item); }}
          style={taskCardIconButtonStyle("success")}
        >
          <TaskCompleteIcon />
        </button>
      ) : null}
      {task.status === "waiting_user" || task.status === "pending" ? (
        <button
          type="button"
          title={t("task.runNow")}
          aria-label={t("task.runNow")}
          onClick={(event) => { event.stopPropagation(); onRunNow(item); }}
          style={taskCardIconButtonStyle("accent")}
        >
          <TaskRunNowIcon />
        </button>
      ) : null}
      {task.status === "running" || task.status === "queued" ? (
        <button
          type="button"
          title={t("task.actionPause")}
          aria-label={t("task.actionPause")}
          onClick={(event) => { event.stopPropagation(); onTogglePause(item); }}
          style={taskCardIconButtonStyle("default")}
        >
          <TaskPauseIcon />
        </button>
      ) : null}
      {task.status === "paused" ? (
        <button
          type="button"
          title={t("task.actionResume")}
          aria-label={t("task.actionResume")}
          onClick={(event) => { event.stopPropagation(); onTogglePause(item); }}
          style={taskCardIconButtonStyle("accent")}
        >
          <TaskResumeIcon />
        </button>
      ) : null}
    </div>
  );
}
