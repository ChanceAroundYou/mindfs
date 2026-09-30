import React from "react";
import { PromptEditor } from "./PromptEditor";
import { StageOptionsBar, type SessionReusePolicy } from "./StageOptionsBar";
import { has1MSuffix, resolveEffortOnSwitch, resolveLongContextOnSwitch, strip1MSuffix, with1MSuffix } from "./action/modelUtils";
import { composerInputStyle } from "./action/composerStyles";
import { useI18n } from "../i18n";
import { DEFAULT_TASK_AGENT } from "../app/appTask";
import type { StageTemplate } from "../services/tasks";
import type { AgentStatus } from "../services/agents";

export type StageEditorProps = {
  stage: StageTemplate;
  onChange: (patch: Partial<StageTemplate>) => void;
  agents: AgentStatus[];

  /** 段名输入的 placeholder；不传则不渲染段名（新建任务面板那一段没有名字概念） */
  stageNamePlaceholder?: string;
  onStageNameChange?: (name: string) => void;

  /**
   * 标题行最左边的自定义控件，插在段名前面。
   * 新建任务面板的任务名就挂在这儿 —— 它和 user 开关属于同一行的视觉组，
   * 曾经在 App.tsx 外面自己占一行，user 芯片被挤到下一排去了。
   */
  leading?: React.ReactNode;

  /** 不传 = 该段角色不可切换（首段固定 user） */
  onToggleRole?: () => void;
  /** 计划模式是否不可选（acp 协议等） */
  planModeDisabled?: boolean;

  /**
   * 这是首段（任务输入）吗？首段额外显示「立即执行」（建完就开跑），
   * 其余段显示「自动进入下一阶段」。user 段的自动推进由引擎固定（反馈完就推进），
   * 面板上不给开关。
   */
  isFirstStage?: boolean;

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
  leading,
  onToggleRole,
  planModeDisabled,
  isFirstStage = false,
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
  const { t } = useI18n();
  const isAgent = stage.role === "agent";
  const value = displayValue ?? stage.prompt_template ?? "";

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "8px", minWidth: 0 }}>
      <div style={{ display: "flex", alignItems: "center", gap: "6px", flexWrap: "wrap" }}>
        {leading}
        {stageNamePlaceholder ? (
          <input
            value={stage.name || ""}
            onChange={(event) => onStageNameChange?.(event.target.value)}
            placeholder={stageNamePlaceholder}
            style={{ ...composerInputStyle, height: "26px", width: "180px", flex: "0 0 180px", fontWeight: 800 }}
          />
        ) : null}
        {/* user 开关贴着段名：亮 = user 段。不给 onToggleRole 就是这一段角色锁死
            （首段固定 user），但仍然显示，别让人以为少了开关。 */}
        <button
          type="button"
          disabled={!onToggleRole}
          onClick={onToggleRole}
          aria-pressed={!isAgent}
          title={isAgent ? t("taskTemplate.userStageOff") : t("taskTemplate.userStageOn")}
          style={roleToggleStyle(!isAgent, !onToggleRole)}
        >
          user
        </button>
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
        agent={stage.agent || DEFAULT_TASK_AGENT}
        model={stage.model || ""}
        effort={stage.effort || ""}
        agentMode={stage.mode || ""}
        fastService={stage.fast_service === "on" || stage.fast_service === "off" ? stage.fast_service : ""}
        onAgentChange={(nextAgent, nextModel) => {
          const status = agents.find((item) => item.name === nextAgent) || null;
          const nextBaseModel = String(nextModel || "").trim();
          const nextModelInfo = status?.models?.find(
            (item) => item.id === nextBaseModel || strip1MSuffix(item.id) === strip1MSuffix(nextBaseModel),
          );
          const nextAvailableEfforts = nextModelInfo?.efforts ?? status?.efforts ?? [];
          onChange({
            agent: nextAgent,
            // 面板把 [1M] 直接存在 model 串里，而下拉回传的是裸 id ——
            // 直接写 nextModel 等于用户没主动关也被清掉 1M。按继承规则把后缀补回去。
            model: with1MSuffix(
              nextBaseModel,
              resolveLongContextOnSwitch({
                nextAgent,
                nextModel: nextBaseModel,
                prevLongContext: has1MSuffix(stage.model || ""),
              }),
            ),
            mode: status?.current_mode_id || "",
            // 原来这里一律置空：换一次模型就把用户手选的 effort 抹掉。
            // 只在目标模型不支持该值时才回落默认值。
            effort: resolveEffortOnSwitch({
              nextAgent,
              nextModel: nextBaseModel,
              prevEffort: stage.effort || "",
              defaultEffort: status?.default_effort || "",
              availableEfforts: nextAvailableEfforts,
            }),
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
            startImmediately={isFirstStage ? stage.start_immediately === true : undefined}
            planMode={!planModeDisabled && stage.plan_mode === true}
            planModeDisabled={planModeDisabled}
            sessionReusePolicy={(stage.session_reuse_policy as SessionReusePolicy) || "task_main"}
            readOnly={mode !== "editable"}
            onAutoAdvanceChange={(next) => onChange({ auto_advance: next })}
            onStartImmediatelyChange={(next) => onChange({ start_immediately: next })}
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

/** user 开关：亮 = user 段。与选项条同款的紧凑尺寸。 */
function roleToggleStyle(active: boolean, disabled?: boolean): React.CSSProperties {
  return {
    height: "26px",
    padding: "0 9px",
    flex: "0 0 auto",
    border: "1px solid var(--border-color)",
    borderRadius: "6px",
    background: active ? "var(--accent-color)" : "var(--input-bg)",
    color: active ? "#fff" : "var(--text-secondary)",
    fontSize: "11px",
    fontWeight: 800,
    cursor: disabled ? "not-allowed" : "pointer",
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    opacity: disabled ? 0.6 : 1,
  };
}
