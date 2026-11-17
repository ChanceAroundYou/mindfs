import React, { useState } from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../../i18n/index";
import type { WorkflowChoice } from "../../shared/taskComments";
import { composerIconButtonStyle } from "../action/composerStyles";
import { ChevronDownSmallIcon, WorkflowIcon } from "./taskIcons";
import { WorkflowManualPanel } from "./WorkflowOptionList";
import { useWorkflowSelectorMenu } from "./useWorkflowSelectorMenu";

export type WorkflowSelectorProps = {
  choice: WorkflowChoice;
  versionId: string;
  onChange: (choice: WorkflowChoice, versionId: string) => void;
};

/**
 * 任务对话输入框里的工作流选择器。
 *
 * 收起时只是一个图标按钮（能代表工作流的分支图），点开是一级菜单：
 * 自动（默认）/ 禁用 / 手动选择。「手动选择」再向右侧展开二级列表，
 * 与 AgentSelector 的「选 agent → 选模型」是同一种交互。
 *
 * 菜单 portal 到 body：任务详情面板正文是滚动容器，内联菜单会被裁掉。
 */
export function WorkflowSelector({ choice, versionId, onChange }: WorkflowSelectorProps) {
  const { t } = useI18n();
  const { triggerRef, menuRef, menuStyle, open, setOpen } = useWorkflowSelectorMenu();
  const [manualOpen, setManualOpen] = useState(choice === "specified");
  // 选择器提交的是 workflow id，但回显要给用户看名字：名字只在 id 仍然对得上时生效。
  const [picked, setPicked] = useState<{ id: string; name: string } | null>(null);

  const pickedName = picked && picked.id === versionId ? picked.name : "";
  const triggerLabel =
    choice === "specified"
      ? pickedName || t("task.workflowChoice.manual")
      : choice === "disabled"
        ? t("task.workflowChoice.none")
        : t("task.workflowChoice.auto");

  const close = () => setOpen(false);

  return (
    <div ref={triggerRef} data-testid="workflow-selector" style={{ display: "flex" }}>
      <button
        type="button"
        data-testid="workflow-selector-trigger"
        data-choice={choice}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`${t("task.workflowChoice")}: ${triggerLabel}`}
        title={`${t("task.workflowChoice")}: ${triggerLabel}`}
        onMouseDown={(event) => event.preventDefault()}
        onClick={() => setOpen(!open)}
        style={{
          ...composerIconButtonStyle(),
          position: "relative",
          color: choice === "specified" ? "var(--accent-color)" : undefined,
        }}
      >
        <WorkflowIcon size={16} />
        {choice !== "empty" ? <span data-testid="workflow-selection-dot" style={dotStyle(choice)} /> : null}
      </button>

      {open && typeof document !== "undefined"
        ? createPortal(
            <div ref={menuRef} onMouseDown={(event) => event.preventDefault()} style={menuStyle}>
              <WorkflowMenu
                choice={choice}
                versionId={versionId}
                pickedName={pickedName}
                manualOpen={manualOpen}
                onPickAuto={() => {
                  onChange("empty", "");
                  close();
                }}
                onPickDisabled={() => {
                  onChange("disabled", "");
                  close();
                }}
                onPickManual={() => setManualOpen((value) => !value)}
                onPickWorkflow={(id, name) => {
                  setPicked({ id, name });
                  onChange("specified", id);
                  close();
                }}
              />
            </div>,
            document.body,
          )
        : null}
    </div>
  );
}

export type WorkflowMenuProps = {
  choice: WorkflowChoice;
  versionId: string;
  pickedName?: string;
  manualOpen: boolean;
  onPickAuto: () => void;
  onPickDisabled: () => void;
  onPickManual: () => void;
  onPickWorkflow: (id: string, name: string) => void;
};

/** 一级三态 + 二级工作流列表。单独导出便于在 node 环境直接渲染测试。 */
export function WorkflowMenu({
  choice,
  versionId,
  pickedName,
  manualOpen,
  onPickAuto,
  onPickDisabled,
  onPickManual,
  onPickWorkflow,
}: WorkflowMenuProps) {
  const { t } = useI18n();
  const manualHint = choice === "specified" && pickedName
    ? pickedName
    : t("task.workflowChoice.manualHint");

  return (
    <div
      role="menu"
      data-testid="workflow-menu"
      aria-label={t("task.workflowChoice")}
      style={menuPanelStyle}
    >
      {/* 二级列表放左列：菜单右缘钉在触发按钮上，展开时一级列不会左右跳。 */}
      {manualOpen ? (
        <div data-testid="workflow-manual-panel" style={listColumnStyle}>
          <WorkflowManualPanel selected={versionId} onSelect={onPickWorkflow} />
        </div>
      ) : null}

      <div style={modeColumnStyle}>
        <button
          type="button"
          role="menuitemradio"
          aria-checked={choice === "empty"}
          data-testid="workflow-choice-auto"
          onClick={onPickAuto}
          style={modeItemStyle(choice === "empty")}
        >
          <span style={modeTitleStyle}>{t("task.workflowChoice.auto")}</span>
          <span style={modeHintStyle}>{t("task.workflowChoice.autoHint")}</span>
        </button>

        <button
          type="button"
          role="menuitemradio"
          aria-checked={choice === "disabled"}
          data-testid="workflow-choice-disabled"
          onClick={onPickDisabled}
          style={modeItemStyle(choice === "disabled")}
        >
          <span style={modeTitleStyle}>{t("task.workflowChoice.none")}</span>
          <span style={modeHintStyle}>{t("task.workflowChoice.noneHint")}</span>
        </button>

        <button
          type="button"
          role="menuitem"
          aria-haspopup="menu"
          aria-expanded={manualOpen}
          data-testid="workflow-choice-manual"
          onClick={onPickManual}
          style={modeItemStyle(choice === "specified")}
        >
          <span style={modeTitleStyle}>{t("task.workflowChoice.manual")}</span>
          <span style={manualHintRowStyle}>
            <span style={modeHintStyle}>{manualHint}</span>
            <span
              aria-hidden="true"
              style={{
                display: "inline-flex",
                flexShrink: 0,
                transform: manualOpen ? "rotate(90deg)" : "rotate(0deg)",
                transition: "transform 0.15s ease",
              }}
            >
              <ChevronDownSmallIcon />
            </span>
          </span>
        </button>
      </div>
    </div>
  );
}

function dotStyle(choice: WorkflowChoice): React.CSSProperties {
  return {
    position: "absolute",
    top: "4px",
    right: "4px",
    width: "6px",
    height: "6px",
    borderRadius: "50%",
    background: choice === "specified" ? "var(--accent-color)" : "var(--text-secondary)",
    boxShadow: "0 0 0 1px var(--panel-bg, #fff)",
  };
}

const menuPanelStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "stretch",
  background: "var(--menu-bg)",
  border: "1px solid var(--menu-border)",
  borderRadius: "12px",
  boxShadow: "0 8px 32px rgba(0,0,0,0.15)",
  padding: "6px 0",
  maxWidth: "calc(100vw - 16px)",
  boxSizing: "border-box",
};

const modeColumnStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 2,
  padding: "0 6px",
  width: "168px",
  boxSizing: "border-box",
};

const modeItemStyle = (active: boolean): React.CSSProperties => ({
  display: "flex",
  flexDirection: "column",
  alignItems: "flex-start",
  gap: 2,
  width: "100%",
  padding: "7px 10px",
  border: "none",
  borderRadius: 8,
  background: active ? "var(--accent-bg, rgba(37, 99, 235, 0.10))" : "transparent",
  color: active ? "var(--accent-color)" : "var(--text-color)",
  textAlign: "left",
  cursor: "pointer",
});

const modeTitleStyle: React.CSSProperties = {
  fontSize: 12,
  fontWeight: 600,
};

const modeHintStyle: React.CSSProperties = {
  fontSize: 11,
  color: "var(--text-secondary)",
  maxWidth: "100%",
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

const manualHintRowStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 4,
  width: "100%",
  minWidth: 0,
};

const listColumnStyle: React.CSSProperties = {
  // 窄屏（手机）上一级列 168px 已经吃掉大半宽度，二级列按剩余空间收缩。
  width: "min(232px, calc(100vw - 200px))",
  maxHeight: "260px",
  overflowY: "auto",
  borderRight: "1px solid var(--menu-divider)",
  padding: "0 6px",
  boxSizing: "border-box",
};