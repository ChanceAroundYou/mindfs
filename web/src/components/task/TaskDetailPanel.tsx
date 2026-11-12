import React from "react";
import { PanelShell, panelButtonStyle } from "../shell/PanelShell";
import { PencilIcon } from "../action/composerIcons";
import { composerInputStyle } from "../action/composerStyles";
import { useI18n } from "../../i18n/index";
import { statusText } from "./taskDetailUtils";
import { pencilStyle, tagStyle } from "./taskDetailStyles";
import { CommentThread } from "./CommentThread";
import { RunViewEntry } from "./RunViewEntry";
import { ConversationView } from "./ConversationView";
import type { TaskDetail } from "../../shared/taskTypes";
import { taskStatusColor } from "../../app/appTask";
import { DeleteIcon, TaskCompleteIcon, TaskFinishWorktreeIcon } from "./taskIcons";
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
    numberLabel,
    saveName,
    finishWorktree,
    completeTask,
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
          <span style={{ fontSize: "12px", fontWeight: 800, color: taskStatusColor(task.status), flexShrink: 0, whiteSpace: "nowrap" }}>
            {statusText(task.status, t)}
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
        <CommentThread rootId={task.root_id} taskId={task.id} nodeId={nodeId} onOpenSession={onOpenSession} />
        <RunViewEntry rootId={task.root_id} taskId={task.id} nodeId={nodeId} onOpenSession={onOpenSession} />
        <ConversationView
          taskId={task.id}
          rootId={task.root_id}
          nodeId={nodeId}
          disabled={terminal}
          onOpenSession={onOpenSession}
        />
      </div>
    </PanelShell>
    </>
  );
}