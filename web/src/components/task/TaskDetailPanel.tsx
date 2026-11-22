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
import { TASK_CONTROL_GAP, taskActionIconStyle } from "./taskStyles";
import { TASK_ACTION_BUTTON_META, taskActionButtons, type TaskActionButtonKey } from "./taskCardActions";
import { taskActionIcons } from "./taskActionIcons";
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
    terminal,
    numberLabel,
    saveName,
    completeTask,
    reopenTask,
    cancelTask,
    archiveTaskCard,
    unarchiveTaskCard,
    deleteTaskCard,
  } = panel;

  // 动作集合只有一份定义：taskActionButtons（看板卡片与详情 header 共用）。
  // 以前详情与卡片各有一套动作口径，两套必然漂移 ——
  // 2026-11-22 用户要求样式/颜色/数量/种类/hover 完全统一，于是收成一份。
  // 这里只负责把动作键翻译成回调；图标取 taskActionIcons，文案与配色取 META。
  const actionHandlers: Record<TaskActionButtonKey, () => void> = {
    "finish-worktree": () => void panel.finishWorktree(),
    pause: () => void panel.pauseTask(),
    resume: () => void panel.resumeTask(),
    complete: () => void completeTask(),
    cancel: () => void cancelTask(),
    reopen: () => void reopenTask(),
    "archive-task": () => void archiveTaskCard(),
    "unarchive-task": () => void unarchiveTaskCard(),
    "delete-task": () => void deleteTaskCard(),
  };
  const actions = taskActionButtons(task).map((key) => ({
    key,
    label: t(TASK_ACTION_BUTTON_META[key].labelKey),
    icon: taskActionIcons[key],
    tone: TASK_ACTION_BUTTON_META[key].tone,
    onSelect: actionHandlers[key],
  }));

  return (
    <>
    <PanelShell
      width={720}
      // 固定高度 + 居中：异步内容补进来时外框不再从底部往上长，打开第一帧就是最终尺寸。
      fixedHeight
      onClose={onClose}
      // 动作键与关闭键之间也取 TASK_CONTROL_GAP；其它面板不传，仍是原来的 8px。
      headerRightGap={TASK_CONTROL_GAP}
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
          {/* 编辑键与任务名之间取统一间距 TASK_CONTROL_GAP（原先是 8px，同一行里
              动作键 4 / 关闭键 8 / 编辑键 8 三种节奏）。 */}
          <span style={{ display: "inline-flex", alignItems: "center", gap: TASK_CONTROL_GAP, minWidth: 0, maxWidth: "100%", flexWrap: "nowrap" }}>
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
        // 动作全部平铺：有几项画几项，删除也在同一排里（不再做独立特例，也不再收进菜单）。
        // 与看板卡片的图标键完全同形同色：同一份键序（taskActionButtons）、同一套图形
        // （taskActionIcons）、同一档配色（taskActionIconStyle）→ 22×22 / 圆角 6；
        // 键间距也取同一条 TASK_CONTROL_GAP（移动端换行时行距一并跟随）。
        <div data-testid="task-detail-actions" style={{ display: "inline-flex", alignItems: "center", justifyContent: "flex-end", flexWrap: "wrap", gap: TASK_CONTROL_GAP }}>
          {actions.map((action) => (
            <button
              key={action.key}
              type="button"
              title={action.label}
              aria-label={action.label}
              disabled={saving}
              onClick={action.onSelect}
              style={{
                ...taskActionIconStyle(action.tone),
                opacity: saving ? 0.4 : 1,
                cursor: saving ? "not-allowed" : "pointer",
              }}
            >
              {action.icon}
            </button>
          ))}
        </div>
      )}
      showCloseButton
    >
      {/* 对话流与下方内容之间留 6px：输入框本身已带 16px 上边距，这里再大就会在
          「输入框 ↔ 执行详情/监管记录胶囊」之间堆出一道死区（用户要求收紧）。
          左右内边距走 .task-detail-content：桌面 14px，移动端再收窄（index.css）。 */}
      <div className="task-detail-content" style={{ display: "flex", flexDirection: "column", gap: "6px", minWidth: 0, flex: "1 0 auto" }}>
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
            human_gate 在对话卡与执行详情里都能审批。两端同一 DOM 顺序（对话流 →
            胶囊行 → 收尾区），不许用 `order` / `position` 改排位：移动端曾把它
            提到正文最前面，用户读成「吸顶横条」。可见性靠首屏滚到底 + 输入区贴底
            对齐保证（conversationScroll.ts / useComposerBottomPin.ts）。 */}
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