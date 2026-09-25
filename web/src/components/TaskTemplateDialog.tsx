import React, { useEffect, useMemo, useState } from "react";
import { StageEditor } from "./StageEditor";
import { PanelShell, panelButtonStyle, panelIconButtonStyle } from "./PanelShell";
import { composerInputStyle } from "./composerStyles";
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

  // user ↔ agent 切换：切过去时用空白段打底，保留段名和 agent 相关的旧值。
  const onToggleStageRole = (index: number) => () => {
    const current = draft.stages[index]?.snapshot;
    if (!current) return;
    if (current.role === "user") {
      const status = agents.find((item) => item.name === (current.agent || "codex")) || agents[0] || null;
      updateStage(index, {
        ...blankAgentStage(),
        name: current.name || "",
        agent: status?.name || current.agent || "codex",
        model: current.model || "",
        effort: current.effort || "",
        fast_service: current.fast_service || "",
        ...(status?.protocol === "acp" ? { plan_mode: false } : {}),
      });
      return;
    }
    updateStage(index, { ...blankUserStage(), name: current.name || "" });
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
    <PanelShell
      width={760}
      minHeight={520}
      onClose={onClose}
      title={<div style={{ fontSize: "15px", fontWeight: 800, color: "var(--text-color)" }}>{title}</div>}
      headerRight={(
        <div style={{ display: "flex", gap: "8px" }}>
          <button type="button" onClick={onClose} style={panelButtonStyle("secondary")}>{t("taskTemplate.close")}</button>
          <button type="button" disabled={saving} onClick={() => void saveTask()} style={panelButtonStyle("primary")}>{saving ? t("common.saving") : t("common.save")}</button>
        </div>
      )}
      belowHeader={saveError ? (
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
    >
      <div style={{ padding: "12px 14px", display: "flex", flexDirection: "column", gap: "12px" }}>
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
                <StageEditor
                  stage={snapshot}
                  agents={agents}
                  onChange={(patch) => updateStage(index, patch)}
                  stageNamePlaceholder={t("taskTemplate.stageNamePlaceholder")}
                  onStageNameChange={(name) => updateStage(index, { name })}
                  onToggleRole={index === 0 ? undefined : onToggleStageRole(index)}
                  planModeDisabled={planModeDisabled}
                  resetKey={`${draft.id || "new"}-${index}`}
                  placeholder={isAgent ? t("taskTemplate.promptTemplate") : t("taskTemplate.userInputTemplate")}
                  label={(
                    <FieldLabelWithInfo
                      label={isAgent ? t("taskTemplate.promptTemplate") : t("taskTemplate.userInputTemplate")}
                      info={isAgent ? t("taskTemplate.promptTemplateInfo") : t("taskTemplate.userInputTemplateInfo")}
                      helpKey={`prompt-${index}`}
                      openHelpKey={openHelpKey}
                      setOpenHelpKey={setOpenHelpKey}
                    />
                  )}
                  actions={(
                    <div style={{ display: "flex", alignItems: "center", justifyContent: "flex-end", gap: "4px", marginLeft: "auto", flex: "0 0 auto" }}>
                      <button
                        type="button"
                        aria-label={t("taskTemplate.deleteStage")}
                        title={index === 0 ? t("taskTemplate.firstStageCannotDelete") : t("taskTemplate.deleteStage")}
                        disabled={index === 0}
                        onClick={() => removeStage(index)}
                        style={{ ...panelIconButtonStyle(true, index === 0) }}
                      >
                        <DeleteIcon />
                      </button>
                    </div>
                  )}
                />
              </div>
            );
          })}
          <button type="button" onClick={addStage} style={{ ...panelButtonStyle("secondary"), flexShrink: 0 }}>{t("taskTemplate.addStage")}</button>
        </div>
    </PanelShell>
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


const fieldStyle: React.CSSProperties = { display: "flex", flexDirection: "column", gap: "4px", minWidth: 0 };
const labelStyle: React.CSSProperties = { fontSize: "11px", color: "var(--text-secondary)", fontWeight: 700 };

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

