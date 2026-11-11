import React from "react";
import { useI18n } from "../../i18n/index";
import { useRefreshSpin } from "../../hooks/useRefreshSpin";
import { PlusSmallIcon, SyncIcon, WorkflowIcon } from "./taskIcons";

type TaskBoardToolbarProps = {
  currentRootId: string;
  onRefresh: () => void | Promise<void>;
  onOpenWorkflowEditor: () => void;
  openTaskCreateDialog: (targetRootId?: string) => void;
};

/**
 * 看板工具栏：新建任务、工作流草稿入口、刷新。
 *
 * 任务模板已彻底下线（Phase 3）：不筛选、不选模板，新建任务一律走内置默认流水。
 */
export function TaskBoardToolbar({
  currentRootId,
  onRefresh,
  onOpenWorkflowEditor,
  openTaskCreateDialog,
}: TaskBoardToolbarProps) {
  const { t } = useI18n();
  const refreshSpin = useRefreshSpin(onRefresh);
  const canCreate = Boolean(currentRootId);

  return (
    <div style={{ display: "flex", alignItems: "center", justifyContent: "flex-end", gap: "6px", padding: "0 0 8px", flexShrink: 0 }}>
      <button
        type="button"
        data-onboarding="task-create"
        title={t("task.create")}
        aria-label={t("task.create")}
        disabled={!canCreate}
        onClick={() => openTaskCreateDialog(currentRootId)}
        style={{
          width: "28px",
          height: "28px",
          borderRadius: "8px",
          border: "none",
          background: "transparent",
          color: canCreate ? "var(--text-color)" : "var(--muted-text)",
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          cursor: canCreate ? "pointer" : "not-allowed",
          flexShrink: 0,
          padding: 0,
          opacity: canCreate ? 1 : 0.55,
        }}
      >
        <PlusSmallIcon />
      </button>
      <button
        type="button"
        data-onboarding="task-workflow-editor"
        title={t("task.workflowEditor.open")}
        aria-label={t("task.workflowEditor.open")}
        onClick={onOpenWorkflowEditor}
        style={{
          width: "28px", height: "28px", borderRadius: "8px", border: "none", background: "transparent",
          color: "var(--text-color)", display: "inline-flex", alignItems: "center", justifyContent: "center",
          cursor: "pointer", flexShrink: 0, padding: 0,
        }}
      >
        <WorkflowIcon />
      </button>
      <button
        type="button"
        data-onboarding="task-refresh"
        title={t("task.refresh")}
        aria-label={t("task.refresh")}
        onClick={() => void refreshSpin.handleClick()}
        onMouseDown={() => refreshSpin.setPressed(true)}
        onMouseUp={() => refreshSpin.setPressed(false)}
        onMouseLeave={() => refreshSpin.setPressed(false)}
        style={{
          width: "22px", height: "28px", borderRadius: "8px", border: "none", background: "transparent",
          color: "var(--text-color)", display: "inline-flex", alignItems: "center", justifyContent: "flex-end",
          cursor: "pointer", flexShrink: 0, padding: 0,
        }}
      >
        <span
          data-task-refresh-visual
          style={{
            width: "18px", height: "28px", borderRadius: "8px",
            background: refreshSpin.pressed || refreshSpin.refreshing ? "rgba(0, 0, 0, 0.06)" : "transparent",
            display: "inline-flex", alignItems: "center", justifyContent: "center",
          }}
        >
          <SyncIcon style={refreshSpin.refreshing ? { animation: "mindfs-update-spin 0.8s linear infinite" } : undefined} />
        </span>
      </button>
    </div>
  );
}