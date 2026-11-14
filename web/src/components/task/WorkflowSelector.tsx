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
 * 对话式任务的工作流选择器：
 * - 默认模式：简洁提示 + "高级选项"链接
 * - 展开模式：三种执行方式竖排显示
 *
 * 设计理念：
 * 1. 90%的用户只需要"发送"，AI自动处理
 * 2. 高级用户可以展开指定工作流或禁用
 * 3. 移动端友好，竖排布局不越界
 */
export function WorkflowSelector({
  choice,
  versionId,
  onChange,
}: WorkflowSelectorProps) {
  const { t } = useI18n();
  const [showPicker, setShowPicker] = useState(false);
  // 选择器提交的是 workflow id，但回显要给用户看名字：把选中的 {id,name} 留在这里，
  // 名字只在 id 仍然对得上时生效，外部改写 versionId 后回退显示 id。
  const [picked, setPicked] = useState<{ id: string; name: string } | null>(null);
  // 展开态只有一个真相源：用户手动点开过，或已经选了非默认项。
  const [manuallyOpened, setManuallyOpened] = useState(false);
  const showAdvanced = manuallyOpened || choice !== "empty";

  return (
    <div className="workflow-selector" data-testid="workflow-selector" style={containerStyle}>
      {/* 默认状态：简洁提示 */}
      {!showAdvanced && (
        <div style={defaultHintStyle}>
          <span style={hintTextStyle}>
            {t("task.workflowAutoMode")}
          </span>
          <button
            type="button"
            onClick={() => setManuallyOpened(true)}
            style={advancedToggleStyle}
            data-testid="workflow-show-advanced"
          >
            {t("task.workflowAdvancedOptions")} ▼
          </button>
        </div>
      )}

      {/* 高级选项：展开后显示三态 */}
      {showAdvanced && (
        <div style={advancedPanelStyle} data-testid="workflow-advanced-panel">
          <div style={advancedHeaderStyle}>
            <span style={headerLabelStyle}>
              {t("task.workflowMode")}
            </span>
            <button
              type="button"
              onClick={() => {
                setManuallyOpened(false);
                onChange("empty", "");
              }}
              style={collapseButtonStyle}
              data-testid="workflow-use-default"
            >
              {t("task.workflowUseDefault")} ▲
            </button>
          </div>

          <div role="radiogroup" aria-label={t("task.workflowChoice")} style={groupStyle}>
            {/* 选项1：智能选择 */}
            <label style={radioLabelStyle(choice === "empty")} data-testid="workflow-choice-auto">
              <input
                type="radio"
                checked={choice === "empty"}
                onChange={() => onChange("empty", "")}
                data-choice="empty"
                style={radioInputStyle}
              />
              <div style={radioContentStyle}>
                <span style={radioTitleStyle}>{t("task.workflowChoice.auto")}</span>
              </div>
            </label>

            {/* 选项2：指定工作流 */}
            <label style={radioLabelStyle(choice === "specified")} data-testid="workflow-choice-manual">
              <input
                type="radio"
                checked={choice === "specified"}
                onChange={() => setShowPicker(true)}
                data-choice="specified"
                style={radioInputStyle}
              />
              <div style={radioContentStyle}>
                <span style={radioTitleStyle}>{t("task.workflowChoice.manual")}</span>
                {versionId && choice === "specified" && (
                  <span style={versionChipStyle}>
                    {picked && picked.id === versionId ? picked.name : versionId}
                  </span>
                )}
              </div>
            </label>

            {/* 选项3：纯对话模式 */}
            <label style={radioLabelStyle(choice === "disabled")} data-testid="workflow-choice-none">
              <input
                type="radio"
                checked={choice === "disabled"}
                onChange={() => onChange("disabled", "")}
                data-choice="disabled"
                style={radioInputStyle}
              />
              <div style={radioContentStyle}>
                <span style={radioTitleStyle}>{t("task.workflowChoice.none")}</span>
              </div>
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
  gap: 8,
};

const defaultHintStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 8,
  flexWrap: "wrap",
  padding: "6px 0",
};

const hintTextStyle: React.CSSProperties = {
  fontSize: 12,
  color: "var(--text-secondary)",
  flex: "1 1 auto",
};

const advancedToggleStyle: React.CSSProperties = {
  fontSize: 11,
  padding: "3px 8px",
  borderRadius: 4,
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--accent-color)",
  cursor: "pointer",
  flexShrink: 0,
};

const advancedPanelStyle: React.CSSProperties = {
  border: "1px solid var(--border-color)",
  borderRadius: 8,
  padding: 12,
  background: "var(--panel-bg)",
  display: "flex",
  flexDirection: "column",
  gap: 10,
};

const advancedHeaderStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  justifyContent: "space-between",
  marginBottom: 4,
};

const headerLabelStyle: React.CSSProperties = {
  fontSize: 12,
  fontWeight: 600,
  color: "var(--text-color)",
};

const collapseButtonStyle: React.CSSProperties = {
  fontSize: 11,
  padding: "2px 6px",
  borderRadius: 4,
  border: "none",
  background: "transparent",
  color: "var(--accent-color)",
  cursor: "pointer",
};

const groupStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 8,
};

const radioLabelStyle = (checked: boolean): React.CSSProperties => ({
  display: "flex",
  alignItems: "flex-start",
  gap: 10,
  padding: "10px 12px",
  borderRadius: 6,
  border: `1.5px solid ${checked ? "var(--accent-color)" : "var(--border-color)"}`,
  background: checked ? "var(--accent-bg, rgba(59, 130, 246, 0.08))" : "transparent",
  cursor: "pointer",
  transition: "all 0.15s ease",
  fontSize: 13,
});

const radioInputStyle: React.CSSProperties = {
  margin: "2px 0 0 0",
  cursor: "pointer",
  flexShrink: 0,
};

const radioContentStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 4,
  flex: 1,
  minWidth: 0,
};

const radioTitleStyle: React.CSSProperties = {
  fontWeight: 500,
  color: "var(--text-color)",
};

const versionChipStyle: React.CSSProperties = {
  fontSize: 11,
  padding: "2px 6px",
  borderRadius: 4,
  background: "var(--tag-bg, rgba(148, 163, 184, 0.2))",
  color: "var(--text-secondary)",
  alignSelf: "flex-start",
  marginTop: 2,
  fontFamily: "monospace",
};
