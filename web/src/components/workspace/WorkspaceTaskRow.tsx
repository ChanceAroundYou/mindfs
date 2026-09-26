import React from "react";
import { useI18n } from "../../i18n";
import { taskCardSurfaceStyle } from "../../app/taskIcons";
import type { SessionItem } from "../../app/appSession";
import type { KanbanTask } from "../../services/tasks";
import { TaskCardRows, type TaskCardAction, type TaskSessionErrorDialog } from "../TaskCardRows";
import type { WorkspaceTaskItem } from "../../app/useWorkspaceBoard";

/**
 * 单条任务卡：卡面 + 两行内容全部来自共享的 TaskCardRows（项目看板同一份），
 * 但**不传 children**，所以任务正文那一段不存在 —— 工作台只回答「各项目在干什么」，
 * 正文要看去项目看板。
 *
 * 和看板的差别只有三处，都是参数而不是代码：
 *   1. 阶段名按**状态**给（执行中/待调度才显示），看板按**列**给；
 *   2. 状态文字**恒显示**且带色 —— 看板只有「已结束」列才显示，工作台没有列的概念；
 *   3. 会话跳转带 root 覆盖，跨项目必需。
 *
 * 点卡片不跳项目看板，就地弹任务详情（见 openWorkspaceTaskDetail）。
 */
export function WorkspaceTaskRow({
  item,
  sessionKeysById,
  sessionByKey,
  getNodeColor,
  onOpen,
  onMove,
  onOpenSession,
  onShowSessionError,
}: {
  item: WorkspaceTaskItem;
  sessionKeysById: Record<string, string[]>;
  sessionByKey: Record<string, SessionItem>;
  getNodeColor: (rootId: string) => string | null;
  onOpen: (item: WorkspaceTaskItem) => void;
  onMove: (item: WorkspaceTaskItem, action: TaskCardAction) => void;
  onOpenSession: (sessionKey: string, rootId: string, taskId: string) => void;
  onShowSessionError: (payload: TaskSessionErrorDialog) => void;
}) {
  const { t } = useI18n();
  const task: KanbanTask = item.task;
  const name = task.name || item.root_name || task.task_template_name || t("task.unnamedTemplate");
  const sessionKeys = sessionKeysById[task.id]?.length
    ? sessionKeysById[task.id]
    : task.main_session_key
      ? [task.main_session_key]
      : [];
  // 看板按列决定要不要显示阶段名（只有「执行中」列给）；工作台没有列，就按状态给。
  const stageName = task.status === "running" || task.status === "queued"
    ? task.current_stage_name || (task.current_stage_index >= 0 ? t("task.stageLabel", { index: task.current_stage_index + 1 }) : "")
    : "";
  return (
    <div
      role="button"
      tabIndex={0}
      onClick={() => onOpen(item)}
      onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); onOpen(item); } }}
      style={{ ...taskCardSurfaceStyle(false), padding: "8px" }}
    >
      <TaskCardRows
        task={task}
        templateNameFallback={name}
        sessionKeys={sessionKeys}
        sessionByKey={sessionByKey}
        getNodeColor={getNodeColor}
        onOpenSession={onOpenSession}
        onMove={(_cardTask, action) => onMove(item, action)}
        onShowSessionError={onShowSessionError}
        stageName={stageName}
        showStatus
      />
    </div>
  );
}
