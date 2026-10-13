import React from "react";
import { useI18n } from "../../i18n/index";
import { useRefreshSpin } from "../../hooks/useRefreshSpin";
import { TASK_TEMPLATE_ALL_FILTER } from "../../shared/taskTemplateConstants";
import { hexToRgba } from "../../shared/hexToRgba";
import { SyncIcon } from "./taskIcons";
import type { TaskTemplate } from "../../shared/taskTypes";
import type { TaskTemplateControls } from "./taskBoardTypes";
import { TaskTemplateActionMenu } from "./TaskTemplateActionMenu";
import { TaskCreateTemplateMenu } from "./TaskCreateTemplateMenu";

type TaskBoardToolbarProps = {
  currentRootId: string;
  isAllTaskTemplateFilter: boolean;
  selectedTaskTemplateForFilter: TaskTemplate | null;
  templateControls: TaskTemplateControls;
  getDisplayNodeColor: (rootId: string) => string | null;
  onRefresh: () => void | Promise<void>;
  openTaskCreateDialog: (template: TaskTemplate | null, targetRootId?: string) => void;
};

export function TaskBoardToolbar({
  currentRootId,
  isAllTaskTemplateFilter,
  selectedTaskTemplateForFilter,
  templateControls,
  getDisplayNodeColor,
  onRefresh,
  openTaskCreateDialog,
}: TaskBoardToolbarProps) {
  const { t } = useI18n();
  const refreshSpin = useRefreshSpin(onRefresh);
  const {
    taskTemplateActionMenuOpen,
    setTaskTemplateActionMenuOpen,
    taskCreateTemplateMenuOpen,
    setTaskCreateTemplateMenuOpen,
    taskTemplates,
    setTaskTemplateFilter,
    openTaskTemplateEditor,
    handleDeleteTaskTemplate,
  } = templateControls;

  return (
    <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 0, padding: "0 0 8px", flexShrink: 0 }}>
      <div style={{ display: "flex", alignItems: "center", gap: "6px", minWidth: 0, flex: 1 }}>
        <div
          role="tablist"
          data-onboarding="task-templates"
          aria-label={t("task.templates")}
          style={{
            display: "flex", alignItems: "center", gap: "2px", overflowX: "auto", padding: "3px",
            borderRadius: "10px", border: "1px solid rgba(100, 116, 139, 0.36)",
            background: "rgba(148, 163, 184, 0.10)", minWidth: 0, scrollbarWidth: "none",
          }}
        >
          {(() => {
            const active = isAllTaskTemplateFilter;
            const color = getDisplayNodeColor(currentRootId) || "var(--accent-color)";
            return (
              <button
                type="button"
                role="tab"
                aria-selected={active}
                onClick={() => {
                  setTaskTemplateFilter(TASK_TEMPLATE_ALL_FILTER);
                  setTaskTemplateActionMenuOpen(false);
                }}
                style={{
                  border: "none", borderRadius: "6px", background: active ? color : "transparent",
                  color: active ? "#fff" : "var(--text-secondary)", padding: "3px 7px", fontSize: "11px",
                  fontWeight: 700, lineHeight: "14px", cursor: "pointer", whiteSpace: "nowrap",
                  boxShadow: active ? `0 1px 3px ${hexToRgba(color, 0.28)}` : "none",
                }}
              >
                {t("task.all")}
              </button>
            );
          })()}
          {taskTemplates.length > 0 ? <span aria-hidden="true" style={{ width: "1px", height: "16px", background: "rgba(100, 116, 139, 0.32)", margin: "0 1px", flexShrink: 0 }} /> : null}
          {taskTemplates.map((template, index) => {
            const templateId = template.id || "";
            const active = selectedTaskTemplateForFilter?.id === templateId;
            const color = getDisplayNodeColor(currentRootId) || "var(--accent-color)";
            return (
              <React.Fragment key={templateId || template.name}>
                {index > 0 ? <span aria-hidden="true" style={{ width: "1px", height: "16px", background: "rgba(100, 116, 139, 0.32)", margin: "0 1px", flexShrink: 0 }} /> : null}
                <button
                  type="button"
                  role="tab"
                  aria-selected={active}
                  onClick={() => {
                    setTaskTemplateFilter(templateId);
                    setTaskTemplateActionMenuOpen(false);
                  }}
                  style={{
                    border: "none", borderRadius: "6px", background: active ? color : "transparent",
                    color: active ? "#fff" : "var(--text-secondary)", padding: "3px 7px", fontSize: "11px",
                    fontWeight: 700, lineHeight: "14px", cursor: "pointer", whiteSpace: "nowrap",
                    boxShadow: active ? `0 1px 3px ${hexToRgba(color, 0.28)}` : "none",
                  }}
                >
                  <span>{template.name || t("task.unnamedTemplate")}</span>
                </button>
              </React.Fragment>
            );
          })}
        </div>
        <TaskTemplateActionMenu
          open={taskTemplateActionMenuOpen}
          onToggle={() => setTaskTemplateActionMenuOpen((open) => !open)}
          selectedTaskTemplateForFilter={selectedTaskTemplateForFilter}
          onCreateTemplate={() => {
            setTaskTemplateActionMenuOpen(false);
            openTaskTemplateEditor(null);
          }}
          onEditTemplate={() => {
            if (!selectedTaskTemplateForFilter) return;
            setTaskTemplateActionMenuOpen(false);
            openTaskTemplateEditor(selectedTaskTemplateForFilter);
          }}
          onDeleteTemplate={() => {
            if (!selectedTaskTemplateForFilter) return;
            setTaskTemplateActionMenuOpen(false);
            void handleDeleteTaskTemplate(selectedTaskTemplateForFilter);
          }}
        />
      </div>
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
      <TaskCreateTemplateMenu
        open={taskCreateTemplateMenuOpen}
        onTriggerClick={() => {
          if (isAllTaskTemplateFilter) {
            setTaskTemplateActionMenuOpen(false);
            setTaskCreateTemplateMenuOpen((open) => !open);
            return;
          }
          openTaskCreateDialog(selectedTaskTemplateForFilter);
        }}
        canCreate={isAllTaskTemplateFilter || !!selectedTaskTemplateForFilter}
        taskTemplates={taskTemplates}
        onCreateTask={(template) => {
          setTaskCreateTemplateMenuOpen(false);
          openTaskCreateDialog(template);
        }}
      />
    </div>
  );
}
