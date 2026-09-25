import React from "react";
import { useI18n } from "../i18n";
import { composerInputStyle } from "./composerStyles";

export type SessionReusePolicy = "task_main" | "same_stage" | "always_new";

export type StageOptions = {
  /** 是否 user 段（user 段没有 agent / 会话复用可言） */
  role: "user" | "agent";
  autoAdvance: boolean;
  planMode: boolean;
  planModeDisabled?: boolean;
  sessionReusePolicy: SessionReusePolicy;
  /** 禁改：首段固定 user、已执行过的段不能改会话复用 */
  readOnly?: boolean;
};

export type StageOptionsBarProps = StageOptions & {
  onToggleRole?: () => void;
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
  onToggleRole,
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
      {/* user 开关：亮 = user 段。首段固定 user，不给切。 */}
      <button
        type="button"
        disabled={disabled || !onToggleRole}
        onClick={onToggleRole}
        aria-pressed={!isAgent}
        title={isAgent ? t("taskTemplate.userStageOff") : t("taskTemplate.userStageOn")}
        style={roleToggleStyle(!isAgent, disabled || !onToggleRole)}
      >
        user
      </button>

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

      {/* 会话复用：agent 段才有意义。 */}
      <label style={{ display: "inline-flex", alignItems: "center", gap: "4px", minWidth: 0 }}>
        <span style={{ fontSize: "11px", color: "var(--text-secondary)", whiteSpace: "nowrap" }}>
          {t("taskTemplate.sessionReuse")}
        </span>
        <select
          value={sessionReusePolicy}
          disabled={disabled || !isAgent}
          aria-label={t("taskTemplate.sessionReuse")}
          onChange={(event) => onSessionReusePolicyChange(event.target.value as SessionReusePolicy)}
          style={{ ...composerInputStyle, height: "26px", width: "auto", cursor: disabled || !isAgent ? "not-allowed" : "pointer" }}
        >
          <option value="task_main">{t("taskTemplate.sessionReuseTaskMain")}</option>
          <option value="same_stage">{t("taskTemplate.sessionReuseSameStage")}</option>
          <option value="always_new">{t("taskTemplate.sessionReuseAlwaysNew")}</option>
        </select>
      </label>
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

export function roleToggleStyle(active: boolean, disabled?: boolean): React.CSSProperties {
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
