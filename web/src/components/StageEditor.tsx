import React from "react";
import { PromptEditor } from "./PromptEditor";
import { StageOptionsBar, type SessionReusePolicy } from "./StageOptionsBar";
import { with1MSuffix } from "./action/modelUtils";
import { composerInputStyle } from "./composerStyles";
import type { StageTemplate } from "../services/tasks";
import type { AgentStatus } from "../services/agents";

export type StageEditorProps = {
  stage: StageTemplate;
  onChange: (patch: Partial<StageTemplate>) => void;
  agents: AgentStatus[];

  /** 段名输入的 placeholder；不传则不渲染段名（新建任务面板那一段没有名字概念） */
  stageNamePlaceholder?: string;
  onStageNameChange?: (name: string) => void;

  /** 不传 = 该段角色不可切换（首段固定 user） */
  onToggleRole?: () => void;
  /** 计划模式是否不可选（acp 协议等） */
  planModeDisabled?: boolean;

  /** editor 上方的字段标签（任务模板编辑有「?」说明，任务详情/新建任务没有） */
  label?: React.ReactNode;

  /** user 段也显示 agent 选择器（新建任务面板：选下一段用哪个 agent/模型） */
  showAgentSelector?: boolean;

  /** 右侧附加控件（删除等） */
  actions?: React.ReactNode;
  /** editor 底部（状态 / 会话入口等） */
  footer?: React.ReactNode;

  editorRef?: React.Ref<import("./editor/TokenEditor").TokenEditorHandle>;
  mode?: "editable" | "readonly" | "done";
  onEdit?: () => void;
  onSend?: () => void;
  onAttach?: () => void;
  sending?: boolean;
  sendDisabled?: boolean;
  placeholder?: string;
  accentColor?: string;
  resetKey?: string | number;
  /** 不可编辑态展示的 prompt（执行过的段用实际渲染值） */
  displayValue?: string;
};

/**
 * 一段阶段的编辑 = 三张任务面板共有的部分。
 *
 * 任务模板编辑、任务详情、新建任务都在编辑同一种东西（阶段定义 + prompt），
 * 以前各拼各的：模板是「段名 + 字段标签 + 选项条 + 编辑器」，详情是
 * 「段名 + 状态 + 编辑器」，新建任务只有一个裸编辑器。这里固定组合
 * 「段名 + StageOptionsBar + PromptEditor」，差异靠 props 表达。
 */
export function StageEditor({
  stage,
  onChange,
  agents,
  stageNamePlaceholder,
  onStageNameChange,
  onToggleRole,
  planModeDisabled,
  label,
  showAgentSelector,
  actions,
  footer,
  editorRef,
  mode = "editable",
  onEdit,
  onSend,
  onAttach,
  sending,
  sendDisabled,
  placeholder,
  accentColor,
  resetKey,
  displayValue,
}: StageEditorProps) {
  const isAgent = stage.role === "agent";
  const value = displayValue ?? stage.prompt_template ?? "";

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "8px", minWidth: 0 }}>
      <div style={{ display: "flex", alignItems: "center", gap: "6px", flexWrap: "wrap" }}>
        {stageNamePlaceholder ? (
          <input
            value={stage.name || ""}
            onChange={(event) => onStageNameChange?.(event.target.value)}
            placeholder={stageNamePlaceholder}
            style={{ ...composerInputStyle, height: "26px", width: "180px", flex: "0 0 180px", fontWeight: 800 }}
          />
        ) : null}
        {actions}
      </div>

      {label ? <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>{label}</div> : null}

      <PromptEditor
        ref={editorRef}
        value={value}
        onChange={mode === "editable" ? (next) => onChange({ prompt_template: next }) : undefined}
        resetKey={resetKey}
        mode={mode}
        role={stage.role}
        showAgentSelector={showAgentSelector}
        placeholder={placeholder}
        accentColor={accentColor}
        onEdit={onEdit}
        onSend={onSend}
        onAttach={onAttach}
        sending={sending}
        sendDisabled={sendDisabled}
        agents={agents}
        agent={stage.agent || "codex"}
        model={stage.model || ""}
        effort={stage.effort || ""}
        agentMode={stage.mode || ""}
        fastService={stage.fast_service === "on" || stage.fast_service === "off" ? stage.fast_service : ""}
        onAgentChange={(nextAgent, nextModel) => {
          const status = agents.find((item) => item.name === nextAgent) || null;
          onChange({
            agent: nextAgent,
            model: nextModel || "",
            mode: status?.current_mode_id || "",
            effort: "",
            fast_service: "",
            ...(status?.protocol === "acp" ? { plan_mode: false } : {}),
          });
        }}
        onModeChange={(mode) => onChange({ mode: mode || "" })}
        onEffortChange={(effort) => onChange({ effort: effort || "" })}
        onFastServiceChange={(fastService) => onChange({ fast_service: fastService })}
        onLongContextChange={(enabled) => onChange({ model: with1MSuffix(stage.model || "", enabled) })}
        header={(
          <StageOptionsBar
            role={stage.role}
            autoAdvance={stage.auto_advance === true}
            planMode={!planModeDisabled && stage.plan_mode === true}
            planModeDisabled={planModeDisabled}
            sessionReusePolicy={(stage.session_reuse_policy as SessionReusePolicy) || "task_main"}
            readOnly={!onToggleRole}
            onToggleRole={onToggleRole}
            onAutoAdvanceChange={(next) => onChange({ auto_advance: next })}
            onPlanModeChange={(next) => {
              if (!planModeDisabled) onChange({ plan_mode: next });
            }}
            onSessionReusePolicyChange={(policy) => onChange({ session_reuse_policy: policy })}
          />
        )}
      />
      {footer}
    </div>
  );
}
