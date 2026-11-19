import React from "react";
import { PanelShell, panelButtonStyle } from "../shell/PanelShell";
import { PencilIcon } from "../action/composerIcons";
import { composerInputStyle } from "../action/composerStyles";
import { useI18n } from "../../i18n/index";
import { pencilStyle, tagStyle } from "./taskDetailStyles";
import { ConversationView } from "./ConversationView";
import { RunViewEntry } from "./RunViewEntry";
import type { TaskDetail } from "../../shared/taskTypes";
import { taskStatusColor, taskStatusLabel } from "../../app/appTask";
import { DeleteIcon, TaskCompleteIcon, TaskFinishWorktreeIcon, TaskReopenIcon } from "./taskIcons";
import { useTaskDetailPanel } from "./useTaskDetailPanel";

export type TaskDetailPanelProps = {
  detail: TaskDetail | null;
  onClose: () => void;
  onOpenSession: (sessionKey: string) => void;
  onMoved?: (detail: TaskDetail) => void;
  nodeId?: string;
};

export function TaskDetailPanel({ detail, onClose, onOpenSession, onMoved, nodeId }: TaskDetailPanelProps) {
  const { t } = useI18n();
  const panel = useTaskDetailPanel({ detail, nodeId, onClose, onMoved, t: t as (key: any, fallback?: any) => string });
  if (!panel || !detail) return null;
  const {
    task,
    editingName,
    setEditingName,
    nameDraft,
    setNameDraft,
    saving,
    canFinishWorktree,
    terminal,
    canCompleteTask,
    canReopenTask,
    numberLabel,
    saveName,
    finishWorktree,
    completeTask,
    reopenTask,
    deleteTaskCard,
  } = panel;

  return (
    <>
    <PanelShell
      width={720}
      onClose={onClose}
      hasUnsavedChanges={() => editingName && nameDraft.trim() !== (task.name || "").trim()}
      title={editingName ? (
        <input
          autoFocus
          value={nameDraft}
          placeholder={t("task.namePlaceholder")}
          onChange={(event) => setNameDraft(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") void saveName();
            if (event.key === "Escape") { setEditingName(false); setNameDraft(task.name || ""); }
          }}
          style={{ ...composerInputStyle, flex: "1 1 auto", minWidth: 0, height: "30px", border: "1px solid var(--accent-color)", fontWeight: 700 }}
        />
      ) : (
        <span style={{ flex: "1 1 auto", minWidth: 0, fontSize: "14px", fontWeight: 700, color: "var(--text-color)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {task.name || t("task.namePlaceholder")}
        </span>
      )}
      headerLeft={(
        <>
          <span data-testid="task-detail-status" style={{ fontSize: "12px", fontWeight: 800, color: taskStatusColor(task.status), flexShrink: 0, whiteSpace: "nowrap" }}>
            {taskStatusLabel(task.status, t)}
          </span>
          {task.task_template_name ? (
            <span style={{ ...tagStyle, flexShrink: 0, whiteSpace: "nowrap" }}>
              {t("task.legacyFlowFromTemplate", { name: task.task_template_name })}
            </span>
          ) : null}
          {numberLabel ? (
            <span style={{ fontSize: "12px", fontWeight: 800, color: "#0ea5e9", flexShrink: 0, whiteSpace: "nowrap" }}>{numberLabel}</span>
          ) : null}
        </>
      )}
      headerRight={editingName ? (
        <>
          <button type="button" disabled={saving} onClick={() => void saveName()} style={panelButtonStyle("primary")}>{t("common.confirm")}</button>
          <button type="button" onClick={() => { setEditingName(false); setNameDraft(task.name || ""); }} style={panelButtonStyle("secondary")}>{t("common.cancel")}</button>
        </>
      ) : (
        <>
          {canFinishWorktree ? (
            <button
              type="button"
              title={t("task.finishWorktree")}
              aria-label={t("task.finishWorktree")}
              disabled={saving}
              onClick={() => void finishWorktree()}
              style={{ ...pencilStyle(false), color: "var(--status-ok)", opacity: saving ? 0.4 : 1 }}
            >
              <TaskFinishWorktreeIcon />
            </button>
          ) : null}
          {canCompleteTask ? (
            <button
              type="button"
              title={t("task.completeShort")}
              aria-label={t("task.complete")}
              disabled={saving}
              onClick={() => void completeTask()}
              style={{ ...pencilStyle(false), color: "var(--status-ok)", opacity: saving ? 0.4 : 1 }}
            >
              <TaskCompleteIcon />
            </button>
          ) : null}
          {canReopenTask ? (
            // 终态任务（完成 / 失败 / 取消）回到待审核：输入框随之重新打开，
            // 上一轮的 Run 与回复都原样留着，直接继续写下一轮。
            <button
              type="button"
              title={t("task.reopen")}
              aria-label={t("task.reopen")}
              disabled={saving}
              onClick={() => void reopenTask()}
              style={{ ...pencilStyle(false), color: "var(--status-warn)", opacity: saving ? 0.4 : 1 }}
            >
              <TaskReopenIcon />
            </button>
          ) : null}
          {terminal ? (
            <button
              type="button"
              title={t("task.deleteTask")}
              aria-label={t("task.deleteTask")}
              disabled={saving}
              onClick={() => void deleteTaskCard()}
              style={{ ...pencilStyle(false), color: "#dc2626", opacity: saving ? 0.4 : 1 }}
            >
              <DeleteIcon />
            </button>
          ) : null}
          <button type="button" aria-label={t("task.renameTask")} title={t("task.renameTask")} onClick={() => setEditingName(true)} style={pencilStyle(false)}>
            <PencilIcon />
          </button>
        </>
      )}
    >
      <div style={{ padding: "12px 14px", display: "flex", flexDirection: "column", gap: "10px" }}>
        {/* 详情面板只有一条对话流（ConversationView）。旧的 CommentThread 读的是同一份
            /api/tasks/{id}/activities，再挂一份列表 + 输入框就是「同一段对话出现两套」。 */}
        <ConversationView
          taskId={task.id}
          rootId={task.root_id}
          nodeId={nodeId}
          disabled={terminal}
          paused={panel.paused}
          onPauseTask={panel.pauseTask}
          onResumeTask={panel.resumeTask}
          onAgentActivityChange={() => void panel.refreshTask()}
          onOpenSession={onOpenSession}
        />
        {/* 运行视图是对话流的下钻（默认收起）；human_gate 在对话卡与运行视图里都能审批。 */}
        <RunViewEntry rootId={task.root_id} taskId={task.id} nodeId={nodeId} refreshKey={task.updated_at} onOpenSession={onOpenSession} />
      </div>
    </PanelShell>
    </>
  );
}