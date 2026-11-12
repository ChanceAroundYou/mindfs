import type React from "react";
import { useState } from "react";
import { useI18n } from "../../i18n/index";
import type { WorkflowRecommendation } from "../../shared/workflowModel";
import { WorkflowPickerDialog } from "./WorkflowPickerDialog";

export type WorkflowChoice = "empty" | "specified" | "disabled";

export type WorkflowSelectorProps = {
  choice: WorkflowChoice;
  versionId: string;
  userInput?: string;
  recommendation?: WorkflowRecommendation | null;
  onChange: (choice: WorkflowChoice, versionId: string) => void;
};

/**
 * 对话式任务创建时的 workflow 三态选择：
 * - empty：交给自动选择（可显示推荐芯片）
 * - specified：手动指定一个已发布 workflow（弹窗里选）
 * - disabled：不跑 workflow，直接对话
 *
 * `versionId` 由组合层持有：本组件只负责提出选择，不保存。
 */
export function WorkflowSelector({
  choice,
  versionId,
  recommendation,
  onChange,
}: WorkflowSelectorProps) {
  const { t } = useI18n();
  const [showPicker, setShowPicker] = useState(false);

  return (
    <div className="workflow-selector" data-testid="workflow-selector">
      <div role="radiogroup" aria-label={t("task.workflowChoice")} style={groupStyle}>
        <button
          type="button"
          role="radio"
          aria-checked={choice === "empty"}
          onClick={() => onChange("empty", "")}
          data-choice="empty"
          style={choice === "empty" ? { ...optionStyle, ...optionSelectedStyle } : optionStyle}
        >
          {t("task.workflowChoice.empty")}
          {recommendation && (
            <span className="recommendation-chip" style={chipStyle}>
              {t("task.workflowRecommended", { name: recommendation.workflow_id })}
            </span>
          )}
        </button>

        <button
          type="button"
          role="radio"
          aria-checked={choice === "specified"}
          onClick={() => setShowPicker(true)}
          data-choice="specified"
          data-version-id={versionId || undefined}
          style={choice === "specified" ? { ...optionStyle, ...optionSelectedStyle } : optionStyle}
        >
          {t("task.workflowChoice.specified")}
          {versionId && <span style={versionStyle}>{versionId}</span>}
        </button>

        <button
          type="button"
          role="radio"
          aria-checked={choice === "disabled"}
          onClick={() => onChange("disabled", "")}
          data-choice="disabled"
          style={choice === "disabled" ? { ...optionStyle, ...optionSelectedStyle } : optionStyle}
        >
          {t("task.workflowChoice.disabled")}
        </button>
      </div>

      {showPicker && (
        <WorkflowPickerDialog
          selected={versionId}
          onSelect={(id) => {
            onChange("specified", id);
            setShowPicker(false);
          }}
          onClose={() => setShowPicker(false)}
        />
      )}
    </div>
  );
}

// userInput 预留给「先输入再推荐」的调用方：推荐结果由上层通过 recommendation 传入，
// 选择器自身不发请求，避免一个纯展示组件被绑到控制面请求上。

const groupStyle: React.CSSProperties = {
  display: "flex",
  flexWrap: "wrap",
  gap: 8,
  alignItems: "center",
};

const optionStyle: React.CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  gap: 6,
  padding: "5px 10px",
  borderRadius: 999,
  border: "1px solid var(--border, rgba(148, 163, 184, 0.45))",
  background: "transparent",
  color: "inherit",
  fontSize: 12,
  cursor: "pointer",
};

const optionSelectedStyle: React.CSSProperties = {
  borderColor: "var(--accent, #3b82f6)",
  boxShadow: "0 0 0 1px var(--accent, #3b82f6) inset",
};

const chipStyle: React.CSSProperties = {
  fontSize: 11,
  padding: "1px 6px",
  borderRadius: 999,
  background: "var(--tag-bg, rgba(148, 163, 184, 0.2))",
};

const versionStyle: React.CSSProperties = { fontSize: 11, opacity: 0.7 };