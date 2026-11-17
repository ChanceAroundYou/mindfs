import type React from "react";
import { useI18n } from "../../i18n/index";
import type { WorkflowMeta } from "../../shared/workflowModel";
import { useWorkflowPicker } from "./useWorkflowPicker";

export type WorkflowOptionListProps = {
  workflows: WorkflowMeta[];
  selected: string;
  /** 传 (id, 显示名)：id 用于提交，name 用于选择器上的回显。 */
  onSelect: (id: string, name: string) => void;
};

/** 纯展示的 workflow 列表；加载/错误分支由容器决定。 */
export function WorkflowOptionList({ workflows, selected, onSelect }: WorkflowOptionListProps) {
  return (
    <div role="list" style={{ display: "flex", flexDirection: "column", gap: 2 }}>
      {workflows.map((workflow) => {
        const active = workflow.id === selected;
        return (
          <button
            key={workflow.id}
            type="button"
            role="menuitemradio"
            aria-checked={active}
            data-workflow-id={workflow.id}
            title={workflow.description || undefined}
            onClick={() => onSelect(workflow.id, workflow.name || workflow.id)}
            style={optionStyle(active)}
          >
            <span style={nameStyle}>{workflow.name || workflow.id}</span>
            {workflow.description ? <span style={descriptionStyle}>{workflow.description}</span> : null}
          </button>
        );
      })}
    </div>
  );
}

export type WorkflowManualPanelProps = {
  selected: string;
  onSelect: (id: string, name: string) => void;
};

/**
 * 「手动选择」的二级列表：只有展开时才挂载，因此列表请求不会随输入框常驻。
 * 取数经 colocated hook，视图层不 import services。
 */
export function WorkflowManualPanel({ selected, onSelect }: WorkflowManualPanelProps) {
  const { t } = useI18n();
  const { workflows, loading, failed } = useWorkflowPicker();

  if (loading) return <div style={hintStyle}>{t("common.loading")}</div>;
  if (failed) return <div role="alert" style={hintStyle}>{t("common.loadingFailed")}</div>;
  if (workflows.length === 0) return <div style={hintStyle}>{t("task.noAvailableWorkflows")}</div>;
  return <WorkflowOptionList workflows={workflows} selected={selected} onSelect={onSelect} />;
}

function optionStyle(active: boolean): React.CSSProperties {
  return {
    display: "flex",
    flexDirection: "column",
    alignItems: "flex-start",
    gap: 1,
    width: "100%",
    padding: "7px 10px",
    border: "none",
    borderRadius: 8,
    background: active ? "var(--accent-bg, rgba(37, 99, 235, 0.10))" : "transparent",
    color: active ? "var(--accent-color)" : "var(--text-color)",
    textAlign: "left",
    cursor: "pointer",
    fontSize: 12,
  };
}

const nameStyle: React.CSSProperties = {
  fontWeight: 600,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
  maxWidth: "100%",
};

const descriptionStyle: React.CSSProperties = {
  color: "var(--text-secondary)",
  fontSize: 11,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
  maxWidth: "100%",
};

const hintStyle: React.CSSProperties = {
  padding: "8px 10px",
  fontSize: 12,
  color: "var(--text-secondary)",
  whiteSpace: "nowrap",
};