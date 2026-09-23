import React, { useEffect, useMemo, useRef, useState } from "react";
import { AgentIcon } from "./AgentIcon";
import { AgentSelector } from "./AgentSelector";
import { has1MSuffix, with1MSuffix } from "./ActionBar";
import TokenEditor, { type TokenEditorHandle } from "./editor/TokenEditor";
import { useI18n, type I18nContextValue } from "../i18n";
import {
  addTaskStage,
  renameTask,
  updateTaskStage,
  type StageTemplate,
  type TaskDetail,
} from "../services/tasks";
import type { AgentStatus } from "../services/agents";
import { reportError } from "../services/error";

export type TaskDetailPanelProps = {
  detail: TaskDetail | null;
  agents: AgentStatus[];
  onClose: () => void;
  onOpenSession: (sessionKey: string) => void;
  onMoved?: (detail: TaskDetail) => void;
  nodeId?: string;
};

const statusColors: Record<string, string> = {
  pending: "var(--text-secondary)",
  running: "#2563eb",
  waiting_user: "#b45309",
  paused: "var(--text-secondary)",
  success: "#16a34a",
  fail: "#dc2626",
  cancelled: "var(--text-secondary)",
  approved: "#16a34a",
  rejected: "#dc2626",
};

function statusText(status: string, t: (key: Parameters<I18nContextValue["t"]>[0]) => string): string {
  switch (status) {
    case "pending": return t("task.status.pending");
    case "running": return t("task.status.running");
    case "waiting_user": return t("task.status.waitingUser");
    case "paused": return t("task.status.paused");
    case "queued": return t("task.status.running");
    case "success": return t("task.status.success");
    case "fail": return t("task.status.fail");
    case "cancelled": return t("task.status.cancelled");
    case "approved": return t("task.status.approved");
    case "rejected": return t("task.status.rejected");
    default: return status;
  }
}

// 只读展示未执行阶段的 prompt：复用 TokenEditor 的排版，但禁输入/禁粘贴，避免 @/#// 候选逻辑介入。
function ReadOnlyPrompt({ text, dimmed }: { text: string; dimmed: boolean }) {
  return (
    <div
      aria-readonly="true"
      style={{
        minHeight: "44px",
        maxHeight: "240px",
        overflowY: "auto",
        borderRadius: "8px",
        border: "1px solid var(--border-color)",
        background: dimmed ? "rgba(148, 163, 184, 0.10)" : "var(--input-bg)",
        color: dimmed ? "var(--text-secondary)" : "var(--text-color)",
        padding: "10px 14px",
        fontSize: "14px",
        lineHeight: "20px",
        whiteSpace: "pre-wrap",
        wordBreak: "break-word",
        userSelect: "text",
      }}
    >
      {text || <span style={{ color: "var(--text-secondary)" }}>—</span>}
    </div>
  );
}

export function TaskDetailPanel({ detail, agents, onClose, onOpenSession, onMoved, nodeId }: TaskDetailPanelProps) {
  const { t } = useI18n();
  const task = detail?.task || null;

  // 任务改名：文本 ↔ 编辑态，确认后提交
  const [nameDraft, setNameDraft] = useState("");
  const [editingName, setEditingName] = useState(false);
  // 阶段卡编辑态：一次改名字+模型+prompt，点发送一起存
  const [editingStage, setEditingStage] = useState(-1);
  const [editName, setEditName] = useState("");
  const [editPrompt, setEditPrompt] = useState("");
  const [editAgent, setEditAgent] = useState("codex");
  const [editModel, setEditModel] = useState("");
  const [editEffort, setEditEffort] = useState("");
  const [editMode, setEditMode] = useState("");
  const [saving, setSaving] = useState(false);
  const editPromptRef = useRef<TokenEditorHandle | null>(null);

  useEffect(() => {
    setNameDraft(task?.name || "");
    setEditingName(false);
    setEditingStage(-1);
  }, [task?.id]);

  useEffect(() => {
    if (!editingName) setNameDraft(task?.name || "");
  }, [task?.name, editingName]);

  useEffect(() => {
    if (editingStage < 0) return;
    window.setTimeout(() => editPromptRef.current?.setText(editPrompt), 0);
    // 仅在切换编辑对象时灌入一次，后续由 TokenEditor 自身维护
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editingStage]);

  const stages = useMemo(() => task?.stages || [], [task?.stages]);
  if (!task || !detail) return null;

  const latestRunFor = (index: number) =>
    (detail.stage_runs || []).filter((run) => run.stage_index === index)
      .sort((a, b) => (a.updated_at < b.updated_at ? 1 : -1))[0] || null;

  const apply = (next: TaskDetail) => onMoved?.(next);
  const fail = (err: unknown) => reportError("file.write_failed", String((err as Error)?.message || ""));

  const saveName = async () => {
    const rootId = task.root_id;
    const next = nameDraft.trim();
    if (!rootId || next === (task.name || "")) {
      setEditingName(false);
      setNameDraft(task.name || "");
      return;
    }
    try {
      setSaving(true);
      apply(await renameTask(rootId, task.id, next, nodeId));
      setEditingName(false);
    } catch (err) { fail(err); } finally { setSaving(false); }
  };

  const startEditStage = (index: number) => {
    const stage = stages[index];
    if (!stage) return;
    setEditingStage(index);
    setEditName(stage.name || "");
    setEditPrompt(stage.prompt_template || "");
    setEditAgent(stage.agent || "codex");
    setEditModel(stage.model || "");
    setEditEffort(stage.effort || "");
    setEditMode(stage.mode || "");
  };

  const saveStage = async (index: number) => {
    const original = stages[index];
    if (!original || !editPrompt.trim()) return;
    const nextStage: StageTemplate = {
      ...original,
      name: editName,
      prompt_template: editPrompt,
      agent: original.role === "agent" ? editAgent : undefined,
      model: original.role === "agent" ? editModel : undefined,
      effort: original.role === "agent" ? editEffort : undefined,
      mode: original.role === "agent" ? editMode : undefined,
    };
    try {
      setSaving(true);
      apply(await updateTaskStage(task.root_id, task.id, index, nextStage, nodeId));
      setEditingStage(-1);
    } catch (err) { fail(err); } finally { setSaving(false); }
  };

  const appendStage = async () => {
    const stage: StageTemplate = {
      name: "",
      role: "agent",
      agent: "codex",
      model: "",
      mode: "",
      effort: "",
      prompt_template: "",
      auto_advance: false,
    };
    try {
      setSaving(true);
      apply(await addTaskStage(task.root_id, task.id, stage, nodeId));
    } catch (err) { fail(err); } finally { setSaving(false); }
  };

  return (
    <div
      style={{ position: "fixed", inset: 0, zIndex: 88, background: "rgba(15, 23, 42, 0.36)", display: "flex", alignItems: "center", justifyContent: "center", padding: "24px" }}
      onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}
    >
      <section style={{ width: "min(720px, 100%)", maxHeight: "88dvh", overflow: "hidden", borderRadius: "10px", background: "var(--menu-bg)", border: "1px solid var(--border-color)", boxShadow: "0 24px 60px rgba(15, 23, 42, 0.24)", display: "flex", flexDirection: "column" }}>
        <header style={{ padding: "10px 14px", borderBottom: "1px solid var(--border-color)", display: "flex", alignItems: "center", gap: "8px", flexShrink: 0 }}>
          {editingName ? (
            <>
              <input
                autoFocus
                value={nameDraft}
                placeholder={t("task.namePlaceholder")}
                onChange={(event) => setNameDraft(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter") void saveName();
                  if (event.key === "Escape") { setEditingName(false); setNameDraft(task.name || ""); }
                }}
                style={{ flex: "1 1 auto", minWidth: 0, height: "30px", borderRadius: "6px", border: "1px solid var(--accent-color)", background: "var(--input-bg)", color: "var(--text-color)", padding: "0 8px", fontSize: "14px", fontWeight: 700, outline: "none" }}
              />
              <button type="button" disabled={saving} onClick={() => void saveName()} style={buttonStyle("primary")}>{t("common.confirm")}</button>
              <button type="button" onClick={() => { setEditingName(false); setNameDraft(task.name || ""); }} style={buttonStyle("secondary")}>{t("common.cancel")}</button>
            </>
          ) : (
            <>
              <span style={{ flex: "1 1 auto", minWidth: 0, fontSize: "14px", fontWeight: 700, color: "var(--text-color)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                {task.task_number ? `#${task.task_number} ` : ""}{task.name || t("task.namePlaceholder")}
              </span>
              <button type="button" aria-label={t("task.renameTask")} title={t("task.renameTask")} onClick={() => setEditingName(true)} style={pencilStyle(false)}>
                <PencilIcon />
              </button>
            </>
          )}
          <span style={{ fontSize: "12px", fontWeight: 800, color: statusColors[task.status] || "var(--text-secondary)", flexShrink: 0 }}>
            {statusText(task.status, t)}
          </span>
          <button type="button" onClick={onClose} style={iconBtn}>{t("taskTemplate.close")}</button>
        </header>

        <div style={{ padding: "12px 14px", overflow: "auto", display: "flex", flexDirection: "column", gap: "10px", minHeight: 0 }}>
          {stages.map((stage, index) => {
            const run = latestRunFor(index);
            const isCurrent = index === task.current_stage_index;
            const executed = !!run && !["pending"].includes(String(run.status));
            const editing = editingStage === index;
            const isAgent = stage.role === "agent";
            const shown = executed ? (run?.rendered_prompt || stage.prompt_template || "") : (stage.prompt_template || "");
            return (
              <div key={stage.id || index} style={{ border: isCurrent ? "1px solid var(--accent-color)" : "1px solid var(--border-color)", borderRadius: "8px", background: "var(--panel-bg)", padding: "10px", display: "flex", flexDirection: "column", gap: "8px" }}>
                <div style={{ display: "flex", alignItems: "center", gap: "8px", flexWrap: "wrap" }}>
                  {editing ? (
                    <input
                      value={editName}
                      onChange={(event) => setEditName(event.target.value)}
                      placeholder={t("taskTemplate.stageNamePlaceholder")}
                      style={{ ...inputStyle, height: "26px", width: "180px", flex: "0 0 180px", fontSize: "12px", fontWeight: 800 }}
                    />
                  ) : (
                    <>
                      <span style={{ fontWeight: 800, fontSize: "12px", color: isCurrent ? "var(--accent-color)" : "var(--text-color)" }}>
                        {stage.name || t("task.stageLabel", { index: index + 1 })}
                      </span>
                      {!executed ? (
                        <button type="button" aria-label={t("common.edit")} title={t("common.edit")} onClick={() => startEditStage(index)} style={pencilStyle(false)}>
                          <PencilIcon />
                        </button>
                      ) : null}
                    </>
                  )}
                  {isAgent ? (
                    <span style={tagStyle}><AgentIcon agentName={stage.agent || "codex"} style={{ width: "12px", height: "12px" }} /> {stage.agent || "codex"}{stage.model ? ` · ${stage.model}` : ""}{stage.effort ? ` · ${stage.effort}` : ""}</span>
                  ) : (
                    <span style={tagStyle}>{t("task.stage.user")}</span>
                  )}
                  <span style={{ fontSize: "11px", fontWeight: 700, color: statusColors[run?.status || ""] || "var(--text-secondary)" }}>
                    {executed ? statusText(run.status, t) : t("task.stage.notExecuted")}
                  </span>
                  {run?.session_key ? (
                    <button type="button" onClick={() => onOpenSession(run.session_key as string)} style={{ ...iconBtn, marginLeft: "auto" }}>
                      {t("task.sessionTitle")}
                    </button>
                  ) : null}
                </div>

                {editing ? (
                  <div style={{ position: "relative" }}>
                    <div style={{ minHeight: "44px", border: "1px solid var(--border-color)", borderRadius: "8px", background: "var(--input-bg)", overflow: "auto" }}>
                      <TokenEditor
                        ref={editPromptRef}
                        placeholder={t("taskTemplate.promptTemplate")}
                        disabled={saving}
                        isDark={false}
                        rightInset={42}
                        topInset={0}
                        bottomInset={12}
                        onChange={(payload) => setEditPrompt(payload.serializedText)}
                      />
                    </div>
                    <div style={{ marginTop: "6px", display: "flex", alignItems: "center", gap: "8px" }}>
                      {isAgent ? (
                        <AgentSelector
                          agent={editAgent}
                          model={editModel}
                          mode={editMode}
                          effort={editEffort}
                          fastService=""
                          longContext={has1MSuffix(editModel || "")}
                          onLongContextChange={(enabled) => setEditModel(with1MSuffix(editModel || "", enabled))}
                          agents={agents}
                          compact
                          menuPlacement="top"
                          showChevron
                          onAgentChange={(agent, model) => {
                            const status = agents.find((item) => item.name === agent) || null;
                            setEditAgent(agent);
                            setEditModel(model || "");
                            setEditMode(status?.current_mode_id || "");
                          }}
                          onModeChange={(mode) => setEditMode(mode || "")}
                          onEffortChange={(effort) => setEditEffort(effort || "")}
                          onFastServiceChange={() => {}}
                        />
                      ) : null}
                      <div style={{ marginLeft: "auto", display: "flex", gap: "8px" }}>
                        <button type="button" onClick={() => setEditingStage(-1)} style={buttonStyle("secondary")}>{t("common.cancel")}</button>
                        <button type="button" disabled={saving || !editPrompt.trim()} onClick={() => void saveStage(index)} style={{ ...buttonStyle("primary"), opacity: !editPrompt.trim() ? 0.5 : 1 }}>
                          {saving ? t("common.saving") : t("common.save")}
                        </button>
                      </div>
                    </div>
                  </div>
                ) : (
                  <ReadOnlyPrompt text={shown} dimmed={executed} />
                )}
              </div>
            );
          })}
          {stages.length === 0 ? (
            <div style={{ fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.noStages")}</div>
          ) : null}

          <button type="button" disabled={saving} onClick={() => void appendStage()} style={{ ...buttonStyle("secondary"), alignSelf: "flex-start" }}>
            {t("task.appendStage")}
          </button>
        </div>
      </section>
    </div>
  );
}

function PencilIcon() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M12 20h9" />
      <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
    </svg>
  );
}

function pencilStyle(disabled: boolean): React.CSSProperties {
  return {
    border: "none",
    background: "transparent",
    color: disabled ? "var(--text-secondary)" : "var(--accent-color)",
    borderRadius: 6,
    width: 24,
    height: 24,
    padding: 0,
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    cursor: disabled ? "not-allowed" : "pointer",
    flexShrink: 0,
    opacity: disabled ? 0.4 : 1,
  };
}

const tagStyle: React.CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  gap: "4px",
  fontSize: "11px",
  fontWeight: 700,
  color: "var(--text-secondary)",
  background: "rgba(148, 163, 184, 0.12)",
  borderRadius: "999px",
  padding: "2px 8px",
};

const iconBtn: React.CSSProperties = {
  height: "28px",
  borderRadius: "6px",
  border: "1px solid var(--border-color)",
  background: "var(--button-bg)",
  color: "var(--text-color)",
  padding: "0 9px",
  fontSize: "12px",
  fontWeight: 700,
  cursor: "pointer",
  whiteSpace: "nowrap",
  flexShrink: 0,
};

const inputStyle: React.CSSProperties = {
  width: "100%",
  boxSizing: "border-box",
  borderRadius: "6px",
  border: "1px solid var(--border-color)",
  background: "var(--input-bg)",
  color: "var(--text-color)",
  padding: "0 8px",
  fontSize: "12px",
  minWidth: 0,
  outline: "none",
};

function buttonStyle(kind: "primary" | "secondary"): React.CSSProperties {
  return {
    height: "30px",
    borderRadius: "6px",
    border: kind === "primary" ? "1px solid var(--accent-color)" : "1px solid var(--border-color)",
    background: kind === "primary" ? "var(--accent-color)" : "var(--button-bg)",
    color: kind === "primary" ? "#fff" : "var(--text-color)",
    padding: "0 12px",
    fontSize: "12px",
    fontWeight: 700,
    cursor: "pointer",
    whiteSpace: "nowrap",
  };
}
