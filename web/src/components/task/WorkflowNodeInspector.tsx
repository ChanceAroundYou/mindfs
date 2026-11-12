import React from "react";
import { useI18n } from "../../i18n/index";
import { panelButtonStyle, panelInputStyle, panelSurfaceStyle } from "../shell/PanelShell";
import {
  LOOP_DEFAULT,
  LOOP_MAX,
  LOOP_MIN,
  type WorkflowNodeDef,
} from "../../shared/workflowModel";
import { AgentSelector } from "../agent/AgentSelector";
import type { AgentStatus } from "../../shared/agentTypes";
import { WorkflowNodeIcon, CloseIcon, LoopNodeIcon } from "./WorkflowNodeIcons";

// 节点属性浮动窗口。从 WorkflowCanvas 拆出来只为守住 500 行硬线，
// 行为与拆前一致：位置由父层算好（画布点击处附近），点外部/Esc 关闭由父层管。

export type WorkflowNodeInspectorProps = {
  node: WorkflowNodeDef;
  position: { x: number; y: number };
  inspectorRef: React.Ref<HTMLDivElement>;
  labels: Record<string, string>;
  dirty: boolean;
  agents: AgentStatus[];
  onClose: () => void;
  onPatch: (patch: Partial<WorkflowNodeDef>) => void;
  onOpenLoop: () => void;
};

export function WorkflowNodeInspector({
  node: selected,
  position,
  inspectorRef,
  labels,
  dirty,
  agents,
  onClose,
  onPatch: patchSelected,
  onOpenLoop,
}: WorkflowNodeInspectorProps) {
  const { t } = useI18n();

  return (
    <div
      ref={inspectorRef}
      data-testid="workflow-canvas-inspector"
      style={{
        position: "fixed",
        left: position.x,
        top: position.y,
        zIndex: 1000,
        maxWidth: "400px",
        maxHeight: "80vh",
        overflowY: "auto",
        ...panelSurfaceStyle(),
        display: "flex",
        flexDirection: "column",
        gap: "8px",
        fontSize: "12px",
        boxShadow: "0 8px 24px rgba(0, 0, 0, 0.2)",
        border: "2px solid var(--accent-color, #3b82f6)",
      }}
    >
      {/* 标题栏 */}
      <div style={{ display: "flex", gap: "8px", alignItems: "center", justifyContent: "space-between", borderBottom: "1px solid var(--border-color)", paddingBottom: "8px" }}>
        <div style={{ display: "flex", gap: "6px", alignItems: "center", flex: 1 }}>
          <WorkflowNodeIcon kind={selected.kind} size={18} />
          <span style={{ fontWeight: 600, color: "var(--accent-color)" }}>
            {labels[selected.kind] || selected.kind}
          </span>
          {dirty ? (
            <span title={t("task.workflowCanvas.unsaved")} style={{ fontSize: "10px", color: "var(--warning-color, #f59e0b)" }}>
              ● 未保存
            </span>
          ) : null}
        </div>
        <button
          type="button"
          onClick={onClose}
          style={{
            ...panelButtonStyle("secondary"),
            padding: "4px 10px",
            minWidth: "unset",
            fontSize: "14px",
            display: "flex",
            alignItems: "center",
          }}
          title="关闭 (Esc)"
        >
          <CloseIcon size={14} />
        </button>
      </div>

      {/* 节点名称 */}
      <div style={{ display: "flex", gap: "6px", alignItems: "center" }}>
        <span style={{ minWidth: "50px" }}>{t("task.workflowCanvas.name")}</span>
        <input
          value={selected.name || ""}
          onChange={(e) => patchSelected({ name: e.target.value })}
          placeholder="节点名称"
          style={{ ...panelInputStyle(), flex: 1 }}
        />
      </div>
      {selected.kind === "agent" ? (
        <>
          {/* Agent 和 Model 选择器 */}
          {agents.length > 0 ? (
            <div style={{ display: "flex", flexDirection: "column", gap: "8px", padding: "8px", background: "var(--button-bg)", borderRadius: "4px" }}>
              <div style={{ fontSize: "11px", fontWeight: 600, color: "var(--text-secondary)" }}>Agent 配置</div>
              <AgentSelector
                agent={String(selected.params?.agent ?? "claude")}
                model={String(selected.params?.model ?? "sonnet")}
                mode=""
                effort=""
                fastService=""
                agents={agents}
                compact
                onAgentChange={(agent, model) => {
                  const updates: Record<string, any> = { agent };
                  if (model) updates.model = model;
                  patchSelected({ params: { ...selected.params, ...updates } });
                }}
                onModeChange={() => {}}
                onEffortChange={() => {}}
                onFastServiceChange={() => {}}
                onLongContextChange={() => {}}
              />
            </div>
          ) : (
            <div style={{ display: "flex", gap: "6px", alignItems: "center" }}>
              <span>{t("task.workflowCanvas.agent")}</span>
              <input
                value={String(selected.params?.agent ?? "")}
                onChange={(e) => patchSelected({ params: { ...selected.params, agent: e.target.value } })}
                style={{ ...panelInputStyle(), flex: 1 }}
              />
              <span>{t("task.workflowCanvas.model")}</span>
              <input
                value={String(selected.params?.model ?? "")}
                onChange={(e) => patchSelected({ params: { ...selected.params, model: e.target.value } })}
                style={{ ...panelInputStyle(), flex: 1 }}
              />
            </div>
          )}

          {/* Prompt 输入 */}
          <div style={{ display: "flex", gap: "6px", alignItems: "flex-start", flexDirection: "column" }}>
            <div style={{ display: "flex", alignItems: "center", gap: "6px", width: "100%" }}>
              <span style={{ fontWeight: 600 }}>{t("task.stage.prompt")}</span>
              <span style={{ fontSize: "10px", color: "var(--text-secondary)", flex: 1 }}>
                {t("task.stage.promptInfo")}
              </span>
            </div>
            <textarea
              value={String(selected.params?.prompt ?? "")}
              onChange={(e) => patchSelected({ params: { ...selected.params, prompt: e.target.value } })}
              placeholder="输入任务提示词..."
              rows={6}
              style={{ ...panelInputStyle(), width: "100%", resize: "vertical", lineHeight: 1.5, fontFamily: "monospace" }}
            />
          </div>

          {/* 工作区写入选项 */}
          <label style={{ display: "flex", gap: "8px", alignItems: "center", padding: "6px", background: "var(--button-bg)", borderRadius: "4px", cursor: "pointer" }}>
            <input
              type="checkbox"
              checked={selected.params?.writes_workspace !== false}
              onChange={(e) => patchSelected({ params: { ...selected.params, writes_workspace: e.target.checked } })}
            />
            <span>{t("task.workflowCanvas.writesWorkspace")}</span>
          </label>
        </>
      ) : null}
      {selected.kind === "loop" ? (
        <>
          <div style={{ padding: "8px", background: "var(--button-bg)", borderRadius: "4px" }}>
            <div style={{ fontSize: "11px", fontWeight: 600, color: "var(--text-secondary)", marginBottom: "8px" }}>循环配置</div>
            <label style={{ display: "flex", gap: "6px", alignItems: "center", marginBottom: "8px" }}>
              <span style={{ minWidth: "80px" }}>{t("task.workflowCanvas.maxIterations")}</span>
              <input
                type="number"
                min={LOOP_MIN}
                max={LOOP_MAX}
                value={Number(selected.params?.max_iterations ?? LOOP_DEFAULT)}
                onChange={(e) => patchSelected({ params: { ...selected.params, max_iterations: Number(e.target.value) } })}
                style={{ ...panelInputStyle(), width: "80px" }}
              />
              <span style={{ fontSize: "10px", color: "var(--text-secondary)" }}>({LOOP_MIN}-{LOOP_MAX})</span>
            </label>
            <label style={{ display: "flex", gap: "6px", flexDirection: "column", alignItems: "stretch" }}>
              <span style={{ fontWeight: 600 }}>{t("task.workflowCanvas.exitExpression")}</span>
              <input
                data-testid="workflow-loop-exit"
                value={String(selected.params?.exit_expression ?? "")}
                placeholder={t("task.workflowCanvas.exitPlaceholder")}
                onChange={(e) => patchSelected({ params: { ...selected.params, exit_expression: e.target.value } })}
                style={{ ...panelInputStyle() }}
              />
              <div style={{ fontSize: "10px", color: "var(--text-secondary)", lineHeight: 1.4 }}>{t("task.workflowCanvas.exitHint")}</div>
            </label>
          </div>
          <button
            type="button"
            data-testid="workflow-loop-enter"
            onClick={onOpenLoop}
            style={{ ...panelButtonStyle("primary"), alignSelf: "stretch", display: "flex", alignItems: "center", justifyContent: "center", gap: "6px" }}
          >
            <LoopNodeIcon size={16} />
            {t("task.workflowCanvas.editInnerGraph")}
          </button>
        </>
      ) : null}
      {selected.kind === "condition" ? (
        <div style={{ padding: "8px", background: "var(--button-bg)", borderRadius: "4px" }}>
          <div style={{ fontSize: "11px", fontWeight: 600, color: "var(--text-secondary)", marginBottom: "8px" }}>条件分支</div>
          <label style={{ display: "flex", gap: "6px", flexDirection: "column", alignItems: "stretch" }}>
            <span>{t("task.workflowCanvas.routes")}</span>
            <input
              value={((selected.params?.routes as string[] | undefined) ?? []).join(",")}
              onChange={(e) =>
                patchSelected({
                  params: { ...selected.params, routes: e.target.value.split(",").map((s) => s.trim()).filter(Boolean) },
                })
              }
              placeholder="pass, fail"
              style={{ ...panelInputStyle() }}
            />
            <div style={{ fontSize: "10px", color: "var(--text-secondary)" }}>用逗号分隔多个分支</div>
          </label>
        </div>
      ) : null}
    </div>
  );
}