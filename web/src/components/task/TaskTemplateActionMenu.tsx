import React from "react";
import { useI18n } from "../../i18n/index";
import { HorizontalDotsIcon, DeleteIcon, EditPencilIcon, PlusSmallIcon, taskTemplateMenuItemStyle } from "./taskIcons";
import type { TaskTemplate } from "../../services/tasks";

export function TaskTemplateActionMenu({
  open,
  onToggle,
  selectedTaskTemplateForFilter,
  onCreateTemplate,
  onEditTemplate,
  onDeleteTemplate,
}: {
  open: boolean;
  onToggle: () => void;
  selectedTaskTemplateForFilter: TaskTemplate | null;
  onCreateTemplate: () => void;
  onEditTemplate: () => void;
  onDeleteTemplate: () => void;
}) {
  const { t } = useI18n();

  return (
    <div style={{ position: "relative", flexShrink: 0 }}>
      <button
        type="button"
        data-onboarding="task-template-menu"
        aria-label={t("task.templateMenu")}
        title={t("task.templateMenu")}
        onClick={onToggle}
        style={{
          width: "28px",
          height: "28px",
          borderRadius: "8px",
          border: "none",
          background: open ? "rgba(0, 0, 0, 0.06)" : "transparent",
          color: "var(--text-secondary)",
          opacity: 1,
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          cursor: "pointer",
          padding: 0,
        }}
      >
        <HorizontalDotsIcon />
      </button>
      {open ? (
        <div
          style={{
            position: "absolute",
            top: "calc(100% + 6px)",
            left: "50%",
            transform: "translateX(-50%)",
            minWidth: "160px",
            padding: "6px",
            borderRadius: "10px",
            border: "1px solid var(--border-color)",
            background: "var(--menu-bg)",
            boxShadow: "0 12px 30px rgba(15, 23, 42, 0.14)",
            zIndex: 40,
          }}
        >
          <button
            type="button"
            onClick={onCreateTemplate}
            style={taskTemplateMenuItemStyle()}
          >
            <PlusSmallIcon />
            <span>{t("task.createTemplate")}</span>
          </button>
          <button
            type="button"
            disabled={!selectedTaskTemplateForFilter}
            onClick={onEditTemplate}
            style={taskTemplateMenuItemStyle(!selectedTaskTemplateForFilter)}
          >
            <EditPencilIcon />
            <span>{t("task.editTemplate")}</span>
          </button>
          <button
            type="button"
            disabled={!selectedTaskTemplateForFilter}
            title={!selectedTaskTemplateForFilter ? t("task.selectTemplate") : t("task.deleteTemplate")}
            onClick={onDeleteTemplate}
            style={taskTemplateMenuItemStyle(!selectedTaskTemplateForFilter)}
          >
            <DeleteIcon />
            <span>{t("task.deleteTemplate")}</span>
          </button>
          <div style={{ height: "1px", background: "var(--border-color)", margin: "6px 2px" }} />
        </div>
      ) : null}
    </div>
  );
}
