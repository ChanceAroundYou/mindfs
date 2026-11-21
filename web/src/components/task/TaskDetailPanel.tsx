import React from "react";
import { PanelShell, panelButtonStyle } from "../shell/PanelShell";
import { PencilIcon } from "../action/composerIcons";
import { composerInputStyle } from "../action/composerStyles";
import { useI18n } from "../../i18n/index";
import { pencilStyle, tagStyle } from "./taskDetailStyles";
import { ConversationView } from "./ConversationView";
import { TaskInspectEntries } from "./TaskInspectEntries";
import { TaskCompletionEntry } from "./TaskCompletionEntry";
import type { TaskDetail } from "../../shared/taskTypes";
import { taskStatusColor, taskStatusLabel } from "../../app/appTask";
import { DeleteIcon, TaskCompleteIcon, TaskFinishWorktreeIcon, TaskReopenIcon } from "./taskIcons";
import { TaskDetailActionsMenu, type TaskDetailAction } from "./TaskDetailActionsMenu";
import { useTaskDetailPanel } from "./useTaskDetailPanel";

export type TaskDetailPanelProps = {
  detail: TaskDetail | null;
  onClose: () => void;
  onOpenSession: (sessionKey: string) => void;
  onMoved?: (detail: TaskDetail) => void;
  /** 任务已在服务端删除：交给看板摘卡（内存 + 缓存）。 */
  onTaskGone?: (rootId: string, taskId: string) => void;
  nodeId?: string;
};

export function TaskDetailPanel({ detail, onClose, onOpenSession, onMoved, onTaskGone, nodeId }: TaskDetailPanelProps) {
  const { t } = useI18n();
  const panel = useTaskDetailPanel({ detail, nodeId, onClose, onMoved, onTaskGone, t: t as (key: any, fallback?: any) => string });
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

  // header 右上角「操作控件」菜单的内容：只有当前真的可做的动作才出现。
  // 删除不进来 —— 它是终态任务的破坏性动作，仍单独摆在 header 上。
  const actions: TaskDetailAction[] = [];
  if (canFinishWorktree) {
    actions.push({
      key: "finish-worktree",
      label: t("task.finishWorktree"),
      icon: <TaskFinishWorktreeIcon />,
      tone: "primary",
      onSelect: () => void finishWorktree(),
    });
  }
  if (canCompleteTask) {
    actions.push({
      key: "complete",
      label: t("task.completeShort"),
      icon: <TaskCompleteIcon />,
      tone: "primary",
      onSelect: () => void completeTask(),
    });
  }
  if (canReopenTask) {
    // 终态任务（完成 / 失败 / 取消）回到待审核：输入框随之重新打开，
    // 上一轮的 Run 与回复都原样留着，直接继续写下一轮。
    actions.push({
      key: "reopen",
      label: t("task.reopen"),
      icon: <TaskReopenIcon />,
      tone: "warn",
      onSelect: () => void reopenTask(),
    });
  }

  return (
    <>
    <PanelShell
      width={720}
      // 对话面板贴底：输入区多打一行时卡片向上长，底部的「执行详情」胶囊原地不动。
      align="end"
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
        <>
          {/* 任务名 + 编辑键是一个整体，标题槽允许换行（PanelShell 的 flexWrap），
              所以必须自己 nowrap：否则长名字会把编辑键挤到第二行、header 变成两行高。
              组内名字 flex 0 1 auto + minWidth 0 按需省略，按钮始终紧跟其后。 */}
          <span style={{ display: "inline-flex", alignItems: "center", gap: "8px", minWidth: 0, maxWidth: "100%", flexWrap: "nowrap" }}>
            <span style={{ flex: "0 1 auto", minWidth: 0, fontSize: "14px", fontWeight: 700, color: "var(--text-color)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
              {task.name || t("task.namePlaceholder")}
            </span>
            <button type="button" aria-label={t("task.renameTask")} title={t("task.renameTask")} onClick={() => setEditingName(true)} style={pencilStyle(false)}>
              <PencilIcon />
            </button>
          </span>
        </>
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
          {/* 收尾 / 完成 / 重新打开都收进「操作控件」菜单，header 只剩这一颗键。 */}
          <TaskDetailActionsMenu actions={actions} disabled={saving} />
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
        </>
      )}
      showCloseButton
    >
      {/* 对话流与下方内容之间留 6px：输入框本身已带 16px 上边距，这里再大就会在
          「输入框 ↔ 执行详情/监管记录胶囊」之间堆出一道死区（用户要求收紧）。
          左右内边距走 .task-detail-content：桌面 14px，移动端再收窄（index.css）。 */}
      <div className="task-detail-content" style={{ display: "flex", flexDirection: "column", gap: "6px", minWidth: 0 }}>
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
        {/* 执行详情 + 监管记录：同一行的两个胶囊，始终存在、展开互斥。
            human_gate 在对话卡与执行详情里都能审批。mobile 下由 CSS `order:-1`
            置顶，否则会被整段对话 + 输入框顶到面板最底部。 */}
        <div className="task-detail-execution">
          <TaskInspectEntries
            rootId={task.root_id}
            taskId={task.id}
            nodeId={nodeId}
            refreshKey={task.updated_at}
            fallbackSessionKey={task.main_session_key}
            taskStatus={task.status}
            onOpenSession={onOpenSession}
          />
        </div>
        <TaskCompletionEntry rootId={task.root_id} taskId={task.id} nodeId={nodeId} refreshKey={task.updated_at} />
      </div>
    </PanelShell>
    </>
  );
}