import React from "react";
import { useI18n } from "../../i18n/index";
import { PlusSmallIcon } from "./taskIcons";
import type { TaskTemplate } from "../../shared/taskTypes";

export function TaskCreateTemplateMenu({
  open,
  onTriggerClick,
  canCreate,
  taskTemplates,
  onCreateTask,
  ref,
}: {
  open: boolean;
  onTriggerClick: () => void;
  canCreate: boolean;
  taskTemplates: TaskTemplate[];
  onCreateTask: (template: TaskTemplate | null) => void;
  /**
   * 点外关闭的判据（useTaskTemplates 的 mousedown 监听读它）。
   * 以前这个 ref 一路传到工具栏却没人接：current 恒为 null，监听把每次
   * mousedown 都当成「点在外面」，菜单在 click 到达前就被卸载 ——
   * 于是「全部」页签下点模板永远弹不出新建面板。
   */
  ref?: React.Ref<HTMLDivElement>;
}) {
  const { t } = useI18n();

  return (
    <div ref={ref} style={{ position: "relative", flexShrink: 0 }}>
      <button
        type="button"
        data-onboarding="task-create"
        title={t("task.create")}
        aria-label={t("task.create")}
        disabled={!canCreate}
        onClick={onTriggerClick}
        style={{
          width: "28px",
          height: "28px",
          borderRadius: "8px",
          border: "none",
          background: open ? "rgba(0, 0, 0, 0.06)" : "transparent",
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
      {open ? (
        <div
          style={{
            position: "absolute",
            top: "calc(100% + 6px)",
            right: 0,
            minWidth: "136px",
            maxWidth: "220px",
            maxHeight: "260px",
            overflowY: "auto",
            padding: "5px",
            borderRadius: "10px",
            border: "1px solid var(--border-color)",
            background: "var(--menu-bg)",
            boxShadow: "0 12px 30px rgba(15, 23, 42, 0.14)",
            zIndex: 40,
          }}
        >
          {taskTemplates.length === 0 ? (
            <div style={{ padding: "8px", fontSize: "12px", color: "var(--text-secondary)", whiteSpace: "nowrap" }}>{t("task.noTemplates")}</div>
          ) : taskTemplates.map((template) => (
            <button
              key={template.id || template.name}
              type="button"
              onClick={() => onCreateTask(template)}
              style={{
                width: "100%",
                minHeight: "28px",
                border: "none",
                borderRadius: "7px",
                background: "transparent",
                color: "var(--text-primary)",
                display: "flex",
                alignItems: "center",
                justifyContent: "flex-start",
                padding: "5px 8px",
                fontSize: "12px",
                fontWeight: 700,
                cursor: "pointer",
                textAlign: "left",
              }}
            >
              <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{template.name || t("task.unnamedTemplate")}</span>
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}
