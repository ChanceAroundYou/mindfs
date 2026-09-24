import React, { useEffect, useMemo, useRef, useState } from "react";
import { PromptEditor } from "./PromptEditor";
import { with1MSuffix } from "./action/modelUtils";
import { composerInputStyle } from "./composerStyles";
import { AgentIcon } from "./AgentIcon";
import {
  saveTaskTemplate,
  type StageTemplate,
  type TaskTemplate,
  type TaskTemplateStage,
} from "../services/tasks";
import type { AgentStatus } from "../services/agents";
import { reportError } from "../services/error";
import { useI18n, type I18nContextValue } from "../i18n";

type TaskTemplateDialogProps = {
  open: boolean;
  agents: AgentStatus[];
  template?: TaskTemplate | null;
  onClose: () => void;
  onSaved?: (template: TaskTemplate) => void;
};

const blankUserStage = (): StageTemplate => ({
  name: "",
  role: "user",
  auto_advance: false,
  prompt_template: "",
});

const blankAgentStage = (): StageTemplate => ({
  name: "",
  role: "agent",
  auto_advance: false,
  agent: "codex",
  model: "",
  mode: "",
  effort: "",
  fast_service: "",
  plan_mode: false,
  session_reuse_policy: "task_main",
  prompt_template: "{previous_input}",
  agent_can_control_stage: false,
});

const newTaskTemplate = (t?: I18nContextValue["t"]): TaskTemplate => ({
  name: "",
  description: "",
  max_concurrency: 2,
  stages: [{ position: 0, snapshot: { ...blankUserStage(), name: defaultStageName(0, t) } }],
});

function defaultStageName(index: number, t?: I18nContextValue["t"]): string {
  return t ? t("taskTemplate.defaultStageName", { index: index + 1 }) : `Stage ${index + 1}`;
}

function normalizeStages(stages: TaskTemplateStage[]): TaskTemplateStage[] {
  return stages.map((stage, index) => ({
    ...stage,
    position: index,
    snapshot: {
      ...stage.snapshot,
      role: index === 0 ? "user" : stage.snapshot.role,
    },
  }));
}

function cloneTemplate(template?: TaskTemplate | null, t?: I18nContextValue["t"]): TaskTemplate {
  const base = template ? { ...template } : newTaskTemplate(t);
  return {
    ...base,
    stages: normalizeStages(base.stages?.length ? base.stages : newTaskTemplate(t).stages),
  };
}

export function TaskTemplateDialog({ open, agents, template, onClose, onSaved }: TaskTemplateDialogProps) {
  const { t } = useI18n();
  const [draft, setDraft] = useState<TaskTemplate>(() => cloneTemplate(template, t));
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState("");
  const [openHelpKey, setOpenHelpKey] = useState("");

  useEffect(() => {
    if (!open) return;
    setDraft(cloneTemplate(template, t));
    setSaveError("");
  }, [open, template, t]);

  const title = useMemo(() => (template?.id ? t("taskTemplate.editTitle") : t("taskTemplate.createTitle")), [template?.id, t]);

  if (!open) return null;

  const updateStage = (index: number, patch: Partial<StageTemplate>) => {
    setDraft((prev) => ({
      ...prev,
      stages: normalizeStages(prev.stages.map((stage, i) => (
        i === index ? { ...stage, snapshot: { ...stage.snapshot, ...patch } } : stage
      ))),
    }));
  };

  const addStage = () => {
    setDraft((prev) => ({
      ...prev,
      stages: normalizeStages([...prev.stages, { position: prev.stages.length, snapshot: { ...blankAgentStage(), name: defaultStageName(prev.stages.length, t) } }]),
    }));
  };

  const removeStage = (index: number) => {
    if (index === 0) return;
    setDraft((prev) => ({
      ...prev,
      stages: normalizeStages(prev.stages.filter((_, stageIndex) => stageIndex !== index)),
    }));
  };

  const saveTask = async () => {
    if (!draft.name.trim()) {
      reportError("file.write_failed", t("taskTemplate.taskTemplateNameRequired"));
      return;
    }
    if (!draft.stages[0] || draft.stages[0].snapshot.role !== "user") {
      reportError("file.write_failed", t("taskTemplate.firstStageMustBeUser"));
      return;
    }
    setSaving(true);
    setSaveError("");
    try {
      const saved = await saveTaskTemplate({
        ...draft,
        stages: normalizeStages(draft.stages),
      });
      setDraft(cloneTemplate(saved, t));
      onSaved?.(saved);
      onClose();
    } catch (err) {
      const message = String((err as Error)?.message || t("taskTemplate.taskTemplateSaveFailed"));
      setSaveError(message);
      reportError("file.write_failed", message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="task-template-overlay" style={{ position: "fixed", inset: 0, zIndex: 90, background: "rgba(15, 23, 42, 0.36)", display: "flex", alignItems: "center", justifyContent: "center", padding: "24px" }}>
      <section className="task-template-dialog" style={{ width: "min(760px, 100%)", maxHeight: "88vh", minHeight: "520px", overflow: "hidden", borderRadius: "10px", background: "var(--menu-bg)", border: "1px solid var(--border-color)", boxShadow: "0 24px 60px rgba(15, 23, 42, 0.24)", display: "flex", flexDirection: "column" }}>
        <style>{`
          .task-template-input:focus {
            border-color: var(--accent-color) !important;
            box-shadow: none;
          }
          @media (max-width: 640px) {
            .task-template-overlay {
              top: 36px !important;
              align-items: flex-start !important;
              padding: 8px 12px 12px !important;
            }
            .task-template-dialog {
              width: 100% !important;
              height: auto !important;
              max-height: 60dvh !important;
            }
            .task-template-dialog-body {
              min-height: 0 !important;
              overflow: auto !important;
              -webkit-overflow-scrolling: touch;
            }
            .task-template-stage-name {
              width: 120px !important;
              flex-basis: 120px !important;
            }
          }
        `}</style>
        <header style={{ padding: "10px 14px", borderBottom: "1px solid var(--border-color)", display: "flex", alignItems: "center", justifyContent: "space-between", gap: "10px" }}>
          <div style={{ fontSize: "15px", fontWeight: 800, color: "var(--text-color)" }}>{title}</div>
          <div style={{ display: "flex", gap: "8px" }}>
            <button type="button" onClick={onClose} style={buttonStyle("secondary")}>{t("taskTemplate.close")}</button>
            <button type="button" disabled={saving} onClick={() => void saveTask()} style={buttonStyle("primary")}>{saving ? t("common.saving") : t("common.save")}</button>
          </div>
        </header>
        {saveError ? (
          <div
            style={{
              margin: "10px 14px 0",
              padding: "8px 10px",
              borderRadius: "8px",
              border: "1px solid rgba(220, 38, 38, 0.24)",
              background: "rgba(220, 38, 38, 0.08)",
              color: "#b91c1c",
              fontSize: "12px",
              lineHeight: 1.45,
              fontWeight: 700,
            }}
          >
            {saveError}
          </div>
        ) : null}
        <div className="task-template-dialog-body" style={{ padding: "12px 14px", overflow: "auto", display: "flex", flexDirection: "column", gap: "12px", minHeight: 0 }}>
          <div style={{ display: "grid", gridTemplateColumns: "minmax(0, 1fr)", gap: "10px", alignItems: "end" }}>
            <label style={fieldStyle}>
              <input className="task-template-input" value={draft.name} placeholder={t("taskTemplate.namePlaceholder")} onChange={(event) => setDraft((prev) => ({ ...prev, name: event.target.value }))} style={{ ...composerInputStyle, height: "30px" }} />
            </label>
          </div>

          {draft.stages.map((stage, index) => {
            const snapshot = stage.snapshot;
            const isAgent = snapshot.role === "agent";
            const selectedAgentStatus = agents.find((item) => item.name === (snapshot.agent || "codex")) || null;
            const planModeDisabled = isAgent && selectedAgentStatus?.protocol === "acp";
            const renderStageMetaActions = () => (
              <div style={{ display: "flex", alignItems: "center", justifyContent: "flex-end", gap: "4px", marginLeft: "auto", flex: "0 0 auto" }}>
                <button
                  type="button"
                  aria-label={t("taskTemplate.deleteStage")}
                  title={index === 0 ? t("taskTemplate.firstStageCannotDelete") : t("taskTemplate.deleteStage")}
                  disabled={index === 0}
                  onClick={() => removeStage(index)}
                  style={{ ...taskIconButtonStyle(index === 0), color: index === 0 ? "var(--text-secondary)" : "#dc2626" }}
                >
                  <DeleteIcon />
                </button>
              </div>
            );
            return (
              <div
                key={`${stage.id || "stage"}-${index}`}
                style={{
                  border: "1px solid rgba(96, 165, 250, 0.42)",
                  borderRadius: "8px",
                  background: "var(--panel-bg)",
                  padding: "10px",
                  display: "flex",
                  flexDirection: "column",
                  gap: "10px",
                  flexShrink: 0,
                }}
              >
                <div style={{ display: "flex", alignItems: "center", gap: "6px", flexWrap: "wrap" }}>
                  <input
                    className="task-template-input task-template-stage-name"
                    value={snapshot.name || ""}
                    onChange={(event) => updateStage(index, { name: event.target.value })}
                    placeholder={t("taskTemplate.stageNamePlaceholder")}
                    style={{ ...composerInputStyle, height: "30px", width: "156px", flex: "0 0 156px" }}
                  />
                  <RoleAgentSwitch
                    role={snapshot.role}
                    disabled={index === 0}
                    agent={snapshot.agent || "codex"}
                    onUserClick={() => updateStage(index, { ...blankUserStage(), name: snapshot.name || "" })}
                    onAgentActivate={() => {
                      const status = agents.find((item) => item.name === (snapshot.agent || "codex")) || agents[0] || null;
                      updateStage(index, {
                        ...blankAgentStage(),
                        name: snapshot.name || "",
                        agent: status?.name || snapshot.agent || "codex",
                        model: snapshot.model || "",
                        effort: snapshot.effort || "",
                        fast_service: snapshot.fast_service || "",
                        ...(status?.protocol === "acp" ? { plan_mode: false } : {}),
                      });
                    }}
                  />
                  <StageOptionsMenu
                    isAgent={isAgent}
                    autoAdvance={snapshot.auto_advance === true}
                    planMode={!planModeDisabled && snapshot.plan_mode === true}
                    planModeDisabled={planModeDisabled}
                    sessionReusePolicy={snapshot.session_reuse_policy || "task_main"}
                    onAutoAdvanceChange={() => updateStage(index, { auto_advance: !snapshot.auto_advance })}
                    onPlanModeChange={() => {
                      if (!planModeDisabled) updateStage(index, { plan_mode: !snapshot.plan_mode });
                    }}
                    onSessionReusePolicyChange={(policy) => updateStage(index, { session_reuse_policy: policy })}
                  />
                  <div style={{ flex: "1 1 8px", minWidth: 0 }} />
                  {renderStageMetaActions()}
                </div>
                <div style={fieldStyle}>
                  <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
                    <FieldLabelWithInfo
                      label={isAgent ? t("taskTemplate.promptTemplate") : t("taskTemplate.userInputTemplate")}
                      info={isAgent ? t("taskTemplate.promptTemplateInfo") : t("taskTemplate.userInputTemplateInfo")}
                      helpKey={`prompt-${index}`}
                      openHelpKey={openHelpKey}
                      setOpenHelpKey={setOpenHelpKey}
                    />
                  </div>
                  {/* 与任务侧同一套 PromptEditor：草稿随改随存，没有发送按钮。
                      agent 选择器由 PromptEditor 自带（role=agent 时才出现），
                      和任务面板、对话输入框是同一个控件，不再另起一套。 */}
                  <PromptEditor
                    value={snapshot.prompt_template || ""}
                    onChange={(value) => updateStage(index, { prompt_template: value })}
                    resetKey={`${draft.id || "new"}-${index}`}
                    role={isAgent ? "agent" : "user"}
                    mode="editable"
                    agent={snapshot.agent || "codex"}
                    model={snapshot.model || ""}
                    effort={snapshot.effort || ""}
                    agentMode={snapshot.mode || ""}
                    fastService={snapshot.fast_service === "on" || snapshot.fast_service === "off" ? snapshot.fast_service : ""}
                    agents={agents}
                    onAgentChange={(nextAgent, nextModel) => {
                      const status = agents.find((item) => item.name === nextAgent) || null;
                      updateStage(index, {
                        agent: nextAgent,
                        model: nextModel || "",
                        mode: status?.current_mode_id || "",
                        effort: "",
                        fast_service: "",
                        ...(status?.protocol === "acp" ? { plan_mode: false } : {}),
                      });
                    }}
                    onModeChange={(mode) => updateStage(index, { mode: mode || "" })}
                    onEffortChange={(effort) => updateStage(index, { effort: effort || "" })}
                    onFastServiceChange={(fastService) => updateStage(index, { fast_service: fastService })}
                    onLongContextChange={(enabled) => updateStage(index, { model: with1MSuffix(snapshot.model || "", enabled) })}
                    placeholder={isAgent ? t("taskTemplate.promptTemplate") : t("taskTemplate.userInputTemplate")}
                  />
                </div>
              </div>
            );
          })}
          <button type="button" onClick={addStage} style={{ ...buttonStyle("secondary"), flexShrink: 0 }}>{t("taskTemplate.addStage")}</button>
        </div>
      </section>
    </div>
  );
}

function FieldLabelWithInfo({ label, info, helpKey, openHelpKey, setOpenHelpKey }: {
  label: string;
  info: string;
  helpKey: string;
  openHelpKey: string;
  setOpenHelpKey: (key: string) => void;
}) {
  const { t } = useI18n();
  const open = openHelpKey === helpKey;
  return (
    <span style={{ ...labelStyle, position: "relative", display: "inline-flex", alignItems: "center", gap: "5px", width: "fit-content" }}>
      {label}
      <button
        type="button"
        aria-label={t("taskTemplate.fieldInfo", { label })}
        onMouseDown={(event) => {
          event.preventDefault();
          event.stopPropagation();
        }}
        onClick={(event) => {
          event.preventDefault();
          event.stopPropagation();
          setOpenHelpKey(open ? "" : helpKey);
        }}
        style={{
          width: "15px",
          height: "15px",
          borderRadius: "999px",
          border: "1px solid var(--border-color)",
          background: open ? "var(--selection-bg)" : "transparent",
          color: open ? "var(--accent-color)" : "var(--text-secondary)",
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          padding: 0,
          fontSize: "10px",
          fontWeight: 800,
          cursor: "pointer",
          lineHeight: 1,
        }}
      >
        i
      </button>
      {open ? (
        <span style={{ color: "var(--text-secondary)", fontSize: "11px", fontWeight: 500, lineHeight: 1.35 }}>
          {info}
        </span>
      ) : null}
    </span>
  );
}

function StageOptionsMenu({
  isAgent,
  autoAdvance,
  planMode,
  planModeDisabled,
  sessionReusePolicy,
  onAutoAdvanceChange,
  onPlanModeChange,
  onSessionReusePolicyChange,
}: {
  isAgent: boolean;
  autoAdvance: boolean;
  planMode: boolean;
  planModeDisabled?: boolean;
  sessionReusePolicy: "task_main" | "same_stage" | "always_new";
  onAutoAdvanceChange: () => void;
  onPlanModeChange: () => void;
  onSessionReusePolicyChange: (policy: "task_main" | "same_stage" | "always_new") => void;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [sessionOpen, setSessionOpen] = useState(false);
  const ref = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!open) return;
    const handlePointerDown = (event: PointerEvent) => {
      if (!ref.current?.contains(event.target as Node)) {
        setOpen(false);
        setSessionOpen(false);
      }
    };
    document.addEventListener("pointerdown", handlePointerDown);
    return () => document.removeEventListener("pointerdown", handlePointerDown);
  }, [open]);

  return (
    <div ref={ref} style={{ position: "relative", width: "32px", height: "30px" }}>
      <button
        type="button"
        aria-label={t("taskTemplate.stageOptions")}
        onClick={() => setOpen((value) => !value)}
        style={menuIconButtonStyle(open)}
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <circle cx="5" cy="12" r="1.8" />
          <circle cx="12" cy="12" r="1.8" />
          <circle cx="19" cy="12" r="1.8" />
        </svg>
      </button>
      {open ? (
        <div style={stageMenuStyle}>
          <MenuCheckRow checked={autoAdvance} label={t("taskTemplate.autoAdvance")} onClick={onAutoAdvanceChange} />
          <MenuCheckRow checked={planMode} label={t("taskTemplate.planMode")} disabled={!isAgent || planModeDisabled} onClick={onPlanModeChange} />
          <div style={menuDividerStyle} />
          <button
            type="button"
            disabled={!isAgent}
            onClick={() => {
              if (isAgent) setSessionOpen((value) => !value);
            }}
            style={menuRowStyle({ disabled: !isAgent })}
          >
            <span style={{ flex: 1 }}>{t("taskTemplate.sessionReuse")}</span>
            <span style={{ color: "var(--text-secondary)", fontSize: "11px" }}>{sessionReuseLabel(sessionReusePolicy, t)}</span>
            <ChevronRight isOpen={sessionOpen} />
          </button>
          {sessionOpen && isAgent ? (
            <>
              <MenuRadioRow checked={sessionReusePolicy === "task_main"} label={t("taskTemplate.sessionReuseTaskMain")} onClick={() => onSessionReusePolicyChange("task_main")} />
              <MenuRadioRow checked={sessionReusePolicy === "same_stage"} label={t("taskTemplate.sessionReuseSameStage")} onClick={() => onSessionReusePolicyChange("same_stage")} />
              <MenuRadioRow checked={sessionReusePolicy === "always_new"} label={t("taskTemplate.sessionReuseAlwaysNew")} onClick={() => onSessionReusePolicyChange("always_new")} />
            </>
          ) : null}
          <div style={menuDividerStyle} />
        </div>
      ) : null}
    </div>
  );
}

function AgentDropdownChevron() {
  return (
    <svg
      width="12"
      height="12"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      style={{ color: "var(--text-secondary)", flexShrink: 0 }}
    >
      <path d="m6 9 6 6 6-6" />
    </svg>
  );
}

function sessionReuseLabel(policy: "task_main" | "same_stage" | "always_new", t: I18nContextValue["t"]): string {
  if (policy === "same_stage") return t("taskTemplate.sessionReuseSameStage");
  if (policy === "always_new") return t("taskTemplate.sessionReuseAlwaysNew");
  return t("taskTemplate.sessionReuseTaskMain");
}

function MenuCheckRow({ checked, label, disabled, onClick }: {
  checked: boolean;
  label: string;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button type="button" disabled={disabled} onClick={onClick} style={menuRowStyle({ active: checked, disabled })}>
      <span>{label}</span>
      <span style={menuTrailingCheckStyle(checked)}>✓</span>
    </button>
  );
}

function MenuRadioRow({ checked, label, onClick }: {
  checked: boolean;
  label: string;
  onClick: () => void;
}) {
  return (
    <button type="button" onClick={onClick} style={menuRowStyle({ active: checked })}>
      <span>{label}</span>
      <span style={menuTrailingCheckStyle(checked)}>✓</span>
    </button>
  );
}

function ChevronRight({ isOpen }: { isOpen: boolean }) {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      style={{
        transform: isOpen ? "rotate(90deg)" : "rotate(0deg)",
        transition: "transform 0.15s cubic-bezier(0.4, 0, 0.2, 1)",
        color: isOpen ? "var(--text-primary)" : "#9ca3af",
        flexShrink: 0,
      }}
      aria-hidden="true"
    >
      <polyline points="9 18 15 12 9 6" />
    </svg>
  );
}

function RoleAgentSwitch({
  role,
  disabled,
  agent,
  onUserClick,
  onAgentActivate,
}: {
  role: "user" | "agent";
  disabled?: boolean;
  agent: string;
  onUserClick: () => void;
  onAgentActivate: () => void;
}) {
  const { t } = useI18n();
  const userActive = role === "user";
  return (
    <div
      style={{
        height: "30px",
        width: "76px",
        borderRadius: "7px",
        border: "1px solid var(--border-color)",
        background: "var(--input-bg)",
        padding: "1px",
        display: "grid",
        gridTemplateColumns: "1fr 1fr",
        opacity: disabled ? 0.68 : 1,
      }}
    >
      <button
        type="button"
        disabled={disabled}
        onClick={onUserClick}
        style={roleSegmentStyle(userActive, disabled)}
      >
        user
      </button>
      <button
        type="button"
        disabled={disabled}
        onClick={onAgentActivate}
        style={roleSegmentStyle(!userActive, disabled)}
        aria-label={t("taskTemplate.switchToAgentStage")}
        title={t("taskTemplate.switchToAgentStage")}
      >
        <AgentIcon agentName={agent || "codex"} style={{ width: "15px", height: "15px" }} />
      </button>
    </div>
  );
}

function roleSegmentStyle(active: boolean, disabled?: boolean): React.CSSProperties {
  return {
    border: "none",
    borderRadius: "5px",
    background: active ? "var(--accent-color)" : "transparent",
    color: active ? "#fff" : "var(--text-color)",
    fontSize: "11px",
    fontWeight: 800,
    cursor: disabled ? "not-allowed" : "pointer",
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    minWidth: 0,
    padding: 0,
  };
}

const fieldStyle: React.CSSProperties = { display: "flex", flexDirection: "column", gap: "4px", minWidth: 0 };
const labelStyle: React.CSSProperties = { fontSize: "11px", color: "var(--text-secondary)", fontWeight: 700 };

function menuIconButtonStyle(active: boolean): React.CSSProperties {
  return {
    width: "30px",
    height: "30px",
    borderRadius: "8px",
    border: "none",
    background: active ? "rgba(0, 0, 0, 0.06)" : "transparent",
    color: "var(--text-secondary)",
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    cursor: "pointer",
    outline: "none",
  };
}

const stageMenuStyle: React.CSSProperties = {
  position: "absolute",
  top: "calc(100% + 6px)",
  right: 0,
  minWidth: "220px",
  padding: "6px",
  borderRadius: "10px",
  border: "1px solid var(--border-color)",
  background: "var(--menu-bg)",
  boxShadow: "0 12px 30px rgba(15, 23, 42, 0.14)",
  zIndex: 25,
};

const menuDividerStyle: React.CSSProperties = {
  height: "1px",
  background: "var(--border-color)",
  margin: "6px 4px",
};

function menuRowStyle({ active = false, disabled = false }: { active?: boolean; disabled?: boolean }): React.CSSProperties {
  return {
    width: "100%",
    minHeight: "32px",
    border: "none",
    borderRadius: "8px",
    background: active ? "var(--selection-bg)" : "transparent",
    color: disabled ? "var(--muted-text)" : active ? "var(--accent-color)" : "var(--text-primary)",
    display: "flex",
    alignItems: "center",
    justifyContent: "space-between",
    gap: "10px",
    padding: "8px 10px",
    fontSize: "12px",
    fontWeight: 500,
    cursor: disabled ? "not-allowed" : "pointer",
    opacity: disabled ? 0.55 : 1,
    textAlign: "left",
  };
}

function menuTrailingCheckStyle(checked: boolean): React.CSSProperties {
  return {
    marginLeft: "auto",
    color: "var(--accent-color)",
    fontSize: "10px",
    opacity: checked ? 1 : 0,
    flexShrink: 0,
  };
}

function taskIconButtonStyle(disabled = false): React.CSSProperties {
  return {
    border: "none",
    background: "transparent",
    color: "var(--text-primary)",
    borderRadius: 6,
    width: 26,
    height: 26,
    padding: 0,
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    cursor: disabled ? "not-allowed" : "pointer",
    flexShrink: 0,
    opacity: disabled ? 0.5 : 1,
  };
}


function DeleteIcon() {
  return (
    <svg
      width="13"
      height="13"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <polyline points="3 6 5 6 21 6" />
      <path d="M19 6l-1 14H6L5 6" />
      <path d="M10 11v6" />
      <path d="M14 11v6" />
      <path d="M9 6V4h6v2" />
    </svg>
  );
}

function buttonStyle(kind: "primary" | "secondary"): React.CSSProperties {
  return {
    height: "30px",
    borderRadius: "6px",
    border: kind === "primary" ? "1px solid var(--accent-color)" : "1px solid var(--border-color)",
    background: kind === "primary" ? "var(--accent-color)" : "var(--button-bg)",
    color: kind === "primary" ? "#fff" : "var(--text-color)",
    padding: "0 9px",
    fontSize: "12px",
    fontWeight: 700,
    cursor: "pointer",
    whiteSpace: "nowrap",
  };
}
