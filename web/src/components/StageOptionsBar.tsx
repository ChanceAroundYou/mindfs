import React from "react";
import { useI18n } from "../i18n";
import { Select } from "./Select";

export type SessionReusePolicy = "task_main" | "same_stage" | "always_new";

export type StageOptions = {
  /** 是否 user 段（user 段没有 agent / 会话复用可言） */
  role: "user" | "agent";
  autoAdvance: boolean;
  planMode: boolean;
  planModeDisabled?: boolean;
  sessionReusePolicy: SessionReusePolicy;
  /** 只读展示（未进入编辑态）：值照常显示，但点不动 */
  readOnly?: boolean;
};

export type StageOptionsBarProps = StageOptions & {
  onAutoAdvanceChange: (next: boolean) => void;
  onPlanModeChange: (next: boolean) => void;
  onSessionReusePolicyChange: (policy: SessionReusePolicy) => void;
};

/**
 * 阶段选项条：user/agent 角色 + 自动进入下一段 + 计划模式 + 会话复用。
 *
 * 任务模板编辑和任务详情里的阶段编辑共用这一套（以前前者把这些塞在三点
 * 菜单里，后者干脆没有）。选项直接摊在编辑区上方，不藏菜单。
 */
export function StageOptionsBar({
  role,
  autoAdvance,
  planMode,
  planModeDisabled,
  sessionReusePolicy,
  readOnly,
  onAutoAdvanceChange,
  onPlanModeChange,
  onSessionReusePolicyChange,
}: StageOptionsBarProps) {
  const { t } = useI18n();
  const isAgent = role === "agent";
  const disabled = !!readOnly;

  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: "6px",
        flexWrap: "wrap",
        minWidth: 0,
      }}
    >
      <OptionToggle
        checked={autoAdvance}
        label={t("taskTemplate.autoAdvance")}
        disabled={disabled}
        onClick={() => onAutoAdvanceChange(!autoAdvance)}
      />
      <OptionToggle
        checked={planMode}
        label={t("taskTemplate.planMode")}
        disabled={disabled || !isAgent || !!planModeDisabled}
        onClick={() => onPlanModeChange(!planMode)}
      />

      {/* 会话复用：agent 段才有意义。自绘下拉，不用系统 option 菜单。
          收起态直接显示当前策略名，不再加「会话复用」前缀 —— 三个选项自解释。 */}
      <div style={{ display: "inline-flex", alignItems: "center", minWidth: "116px" }}>
          <Select
            value={sessionReusePolicy}
            disabled={disabled || !isAgent}
            ariaLabel={t("taskTemplate.sessionReuse")}
            onChange={onSessionReusePolicyChange}
            options={[
              { value: "task_main", label: t("taskTemplate.sessionReuseTaskMain") },
              { value: "same_stage", label: t("taskTemplate.sessionReuseSameStage") },
              { value: "always_new", label: t("taskTemplate.sessionReuseAlwaysNew") },
            ]}
            size="panel"
          />
      </div>
    </div>
  );
}

function OptionToggle({
  checked,
  label,
  disabled,
  onClick,
}: {
  checked: boolean;
  label: string;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      aria-pressed={checked}
      title={label}
      style={optionChipStyle(checked, disabled)}
    >
      {label}
    </button>
  );
}

function optionChipStyle(active: boolean, disabled?: boolean): React.CSSProperties {
  return {
    height: "26px",
    padding: "0 9px",
    flex: "0 0 auto",
    border: `1px solid ${active ? "var(--accent-color)" : "var(--border-color)"}`,
    borderRadius: "6px",
    background: active ? "rgba(37, 99, 235, 0.10)" : "transparent",
    color: active ? "var(--accent-color)" : "var(--text-secondary)",
    fontSize: "11px",
    fontWeight: 700,
    cursor: disabled ? "not-allowed" : "pointer",
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    opacity: disabled ? 0.5 : 1,
    whiteSpace: "nowrap",
  };
}
