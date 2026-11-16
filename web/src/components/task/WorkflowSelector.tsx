import React, { useState } from "react";
import { useI18n } from "../../i18n/index";
import type { WorkflowChoice } from "../../shared/taskComments";
import { WorkflowPickerDialog } from "./WorkflowPickerDialog";

export type WorkflowSelectorProps = {
  choice: WorkflowChoice;
  versionId: string;
  onChange: (choice: WorkflowChoice, versionId: string) => void;
};

/**
 * 对话输入框的工作流选择器。
 *
 * 收起时只留一个胶囊：自动选择 / 指定工作流（或已选名字）/ 纯对话。点开才显示
 * 三个选项；不再解释「AI 会自动选」——那只是噪音。选中的工作流始终显示名字，
 * 不显示 `wf-xxx:v1` 这种内部版本 id。
 */
export function WorkflowSelector({
  choice,
  versionId,
  onChange,
}: WorkflowSelectorProps) {
  const { t } = useI18n();
  const [showPicker, setShowPicker] = useState(false);
  // 选择器提交的是 workflow id，但回显要给用户看名字：把选中的 {id,name} 留在这里，
  // 名字只在 id 仍然对得上时生效，外部改写 versionId 后回退到通用「指定工作流」。
  const [picked, setPicked] = useState<{ id: string; name: string } | null>(null);
  const [open, setOpen] = useState(choice !== "empty");

  const pickedName = picked && picked.id === versionId ? picked.name : t("task.workflowChoice.manual");
  const triggerLabel =
    choice === "specified"
      ? pickedName
      : choice === "disabled"
        ? t("task.workflowChoice.none")
        : t("task.workflowChoice.auto");

  return (
    <div className="workflow-selector" data-testid="workflow-selector" style={containerStyle}>
      <button
        type="button"
        onClick={() => setOpen((value) => !value)}
        style={triggerStyle}
        data-testid="workflow-show-advanced"
        aria-expanded={open}
      >
        {triggerLabel} {open ? "▲" : "▼"}
      </button>

      {open && (
        <div style={advancedPanelStyle} data-testid="workflow-advanced-panel">
          <div role="radiogroup" aria-label={t("task.workflowChoice")} style={groupStyle}>
            <label style={radioLabelStyle(choice === "empty")} data-testid="workflow-choice-auto">
              <input
                type="radio"
                checked={choice === "empty"}
                onChange={() => {
                  onChange("empty", "");
                  setOpen(false);
                }}
                data-choice="empty"
                style={radioInputStyle}
              />
              <span style={radioTitleStyle}>{t("task.workflowChoice.auto")}</span>
            </label>

            <label style={radioLabelStyle(choice === "specified")} data-testid="workflow-choice-manual">
              <input
                type="radio"
                checked={choice === "specified"}
                onChange={() => setShowPicker(true)}
                data-choice="specified"
                style={radioInputStyle}
              />
              <span style={radioTitleStyle}>{t("task.workflowChoice.manual")}</span>
            </label>

            <label style={radioLabelStyle(choice === "disabled")} data-testid="workflow-choice-none">
              <input
                type="radio"
                checked={choice === "disabled"}
                onChange={() => onChange("disabled", "")}
                data-choice="disabled"
                style={radioInputStyle}
              />
              <span style={radioTitleStyle}>{t("task.workflowChoice.none")}</span>
            </label>
          </div>
        </div>
      )}

      {showPicker && (
        <WorkflowPickerDialog
          selected={versionId}
          onSelect={(id, name) => {
            setPicked({ id, name });
            onChange("specified", id);
            setShowPicker(false);
            setOpen(false);
          }}
          onClose={() => setShowPicker(false)}
        />
      )}
    </div>
  );
}

// Styles

const containerStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 6,
  alignItems: "flex-start",
};

const triggerStyle: React.CSSProperties = {
  fontSize: 11,
  padding: "3px 10px",
  borderRadius: 999,
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-secondary)",
  cursor: "pointer",
  maxWidth: "100%",
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

const advancedPanelStyle: React.CSSProperties = {
  border: "1px solid var(--border-color)",
  borderRadius: 8,
  padding: 6,
  background: "var(--panel-bg)",
  display: "flex",
  flexDirection: "column",
  gap: 6,
  width: "100%",
  boxSizing: "border-box",
};

const groupStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 4,
};

const radioLabelStyle = (checked: boolean): React.CSSProperties => ({
  display: "flex",
  alignItems: "center",
  gap: 8,
  padding: "6px 8px",
  borderRadius: 6,
  border: `1px solid ${checked ? "var(--accent-color)" : "transparent"}`,
  background: checked ? "var(--accent-bg, rgba(59, 130, 246, 0.08))" : "transparent",
  cursor: "pointer",
  fontSize: 12,
});

const radioInputStyle: React.CSSProperties = {
  margin: 0,
  cursor: "pointer",
  flexShrink: 0,
};

const radioTitleStyle: React.CSSProperties = {
  fontWeight: 500,
  color: "var(--text-color)",
};
