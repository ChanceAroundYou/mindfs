import type React from "react";
import { useI18n } from "../../i18n/index";
import type { WorkflowMeta } from "../../shared/workflowModel";
import { useWorkflowPicker } from "./useWorkflowPicker";

export type WorkflowPickerListProps = {
  workflows: WorkflowMeta[];
  selected: string;
  onSelect: (id: string) => void;
};

/** 纯展示的选项列表；加载/错误分支留在弹窗里。 */
export function WorkflowPickerList({ workflows, selected, onSelect }: WorkflowPickerListProps) {
  return (
    <ul style={listStyle}>
      {workflows.map((workflow) => {
        const active = workflow.id === selected;
        return (
          <li key={workflow.id} style={itemStyle}>
            <button
              type="button"
              onClick={() => onSelect(workflow.id)}
              data-selected={active ? "true" : undefined}
              style={active ? { ...optionStyle, ...optionSelectedStyle } : optionStyle}
            >
              <strong style={nameStyle}>{workflow.name || workflow.id}</strong>
              {workflow.description && <p style={descriptionStyle}>{workflow.description}</p>}
              {workflow.tags.length > 0 && (
                <div style={tagsStyle}>
                  {workflow.tags.map((tag) => (
                    <span key={tag} className="tag" style={tagStyle}>
                      {tag}
                    </span>
                  ))}
                </div>
              )}
            </button>
          </li>
        );
      })}
    </ul>
  );
}

export type WorkflowPickerDialogProps = {
  selected: string;
  onSelect: (id: string) => void;
  onClose: () => void;
};

/** 选择已发布 workflow 的弹窗。列表通过 colocated hook 拉取（视图不碰 services）。 */
export function WorkflowPickerDialog({ selected, onSelect, onClose }: WorkflowPickerDialogProps) {
  const { t } = useI18n();
  const { workflows, loading, failed } = useWorkflowPicker();

  return (
    <div
      className="workflow-picker-dialog"
      data-testid="workflow-picker"
      role="dialog"
      aria-modal="true"
      aria-label={t("task.selectWorkflow")}
      style={overlayStyle}
    >
      <div className="dialog-overlay" onClick={onClose} style={backdropStyle} />
      <div className="dialog-content" style={contentStyle}>
        <h3 style={titleStyle}>{t("task.selectWorkflow")}</h3>
        {loading ? (
          <div style={hintStyle}>{t("common.loading")}</div>
        ) : failed ? (
          <div role="alert" style={hintStyle}>
            {t("common.loadingFailed")}
          </div>
        ) : workflows.length === 0 ? (
          <div style={hintStyle}>{t("task.noAvailableWorkflows")}</div>
        ) : (
          <WorkflowPickerList workflows={workflows} selected={selected} onSelect={onSelect} />
        )}
        <button type="button" onClick={onClose} style={cancelStyle}>
          {t("common.cancel")}
        </button>
      </div>
    </div>
  );
}

const overlayStyle: React.CSSProperties = {
  position: "fixed",
  inset: 0,
  zIndex: 50,
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  padding: 16,
};

const backdropStyle: React.CSSProperties = {
  position: "absolute",
  inset: 0,
  background: "rgba(15, 23, 42, 0.45)",
};

const contentStyle: React.CSSProperties = {
  position: "relative",
  width: "min(560px, 100%)",
  maxHeight: "80vh",
  overflowY: "auto",
  background: "var(--surface, #fff)",
  color: "var(--text, #0f172a)",
  borderRadius: 12,
  padding: "16px 16px 12px",
  boxShadow: "0 18px 50px rgba(15, 23, 42, 0.28)",
};

const titleStyle: React.CSSProperties = { margin: "0 0 12px", fontSize: 15, fontWeight: 600 };
const hintStyle: React.CSSProperties = { padding: "8px 0", opacity: 0.7, fontSize: 13 };
const listStyle: React.CSSProperties = {
  listStyle: "none",
  margin: 0,
  padding: 0,
  display: "flex",
  flexDirection: "column",
  gap: 8,
};
const itemStyle: React.CSSProperties = { margin: 0 };
const optionStyle: React.CSSProperties = {
  display: "block",
  width: "100%",
  textAlign: "left",
  border: "1px solid var(--border, rgba(148, 163, 184, 0.45))",
  borderRadius: 8,
  background: "transparent",
  color: "inherit",
  padding: "10px 12px",
  cursor: "pointer",
};
const optionSelectedStyle: React.CSSProperties = {
  borderColor: "var(--accent, #3b82f6)",
  boxShadow: "0 0 0 1px var(--accent, #3b82f6) inset",
};
const nameStyle: React.CSSProperties = { fontSize: 13 };
const descriptionStyle: React.CSSProperties = { margin: "4px 0 0", fontSize: 12, opacity: 0.75 };
const tagsStyle: React.CSSProperties = { display: "flex", flexWrap: "wrap", gap: 4, marginTop: 6 };
const tagStyle: React.CSSProperties = {
  fontSize: 11,
  padding: "1px 6px",
  borderRadius: 999,
  background: "var(--tag-bg, rgba(148, 163, 184, 0.2))",
};
const cancelStyle: React.CSSProperties = {
  marginTop: 12,
  padding: "6px 12px",
  borderRadius: 8,
  border: "1px solid var(--border, rgba(148, 163, 184, 0.45))",
  background: "transparent",
  color: "inherit",
  cursor: "pointer",
};