import React, { useEffect, useState } from "react";
import { AgentIcon } from "./AgentIcon";
import { AgentSelector } from "./AgentSelector";
import { useI18n, type I18nContextValue } from "../i18n";
import {
  renameTask,
  rerunTaskStage,
  addTaskStage,
  updateTaskStage,
  type StageTemplate,
} from "../services/tasks";
import type { TaskDetail } from "../services/tasks";
import type { AgentStatus } from "../services/agents";
import { reportError } from "../services/error";

export type TaskDetailPanelProps = {
  detail: TaskDetail | null;
  agents: AgentStatus[];
  onClose: () => void;
  onOpenSession: (sessionKey: string) => void;
  onEditInput: () => void;
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
  const key = status;
  switch (key) {
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
    default: return key;
  }
}

export function TaskDetailPanel({ detail, agents, onClose, onOpenSession, onEditInput, onMoved, nodeId }: TaskDetailPanelProps) {
  const { t } = useI18n();
  const task = detail?.task || null;
  const [nameDraft, setNameDraft] = useState("");
  const [commentPrompt, setCommentPrompt] = useState("");
  const [commentRole, setCommentRole] = useState<StageRoleKey>("agent");
  const [commentAgent, setCommentAgent] = useState("codex");
  const [commentModel, setCommentModel] = useState("");
  const [commentEffort, setCommentEffort] = useState("");
  const [commentMode, setCommentMode] = useState("");
  const [editingStage, setEditingStage] = useState(-1);
  const [editPrompt, setEditPrompt] = useState("");
  const [editName, setEditName] = useState("");
  const [editAgent, setEditAgent] = useState("codex");
  const [editModel, setEditModel] = useState("");
  const [editEffort, setEditEffort] = useState("");
  const [editMode, setEditMode] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setNameDraft(task?.name || "");
    setCommentPrompt("");
    setEditingStage(-1);
  }, [task?.id]);

  useEffect(() => {
    setNameDraft(task?.name || "");
  }, [task?.name]);

  if (!task || !detail) return null;

  const stages = task.stages || [];
  const latestRunFor = (index: number) =>
    (detail.stage_runs || []).filter((run) => run.stage_index === index)
      .sort((a, b) => (a.updated_at < b.updated_at ? 1 : -1))[0] || null;
  const terminal = task.status === "success" || task.status === "fail" || task.status === "cancelled";

  const apply = (next: TaskDetail) => onMoved?.(next);
  const fail = (err: unknown) => reportError("file.write_failed", String((err as Error)?.message || ""));

  const saveName = async () => {
    const rootId = task.root_id;
    if (rootId) {
      try {
        const next = await renameTask(rootId, task.id, nameDraft, nodeId);
        apply(next);
      } catch (err) { fail(err); }
    }
  };

  const submitComment = async () => {
    const prompt = commentPrompt.trim();
    if (!prompt) return;
    const stage: StageTemplate = commentRole === "user"
      ? { name: "", role: "user", prompt_template: prompt, auto_advance: false }
      : { name: "", role: "agent", agent: commentAgent, model: commentModel, effort: commentEffort, mode: commentMode, prompt_template: prompt, auto_advance: false };
    try {
      setSaving(true);
      const next = await addTaskStage(task.root_id, task.id, stage, nodeId);
      apply(next);
      setCommentPrompt("");
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
      const next = await updateTaskStage(task.root_id, task.id, index, nextStage, nodeId);
      apply(next);
      setEditingStage(-1);
    } catch (err) { fail(err); } finally { setSaving(false); }
  };

  const rerun = async (index: number) => {
    try {
      const next = await rerunTaskStage(task.root_id, task.id, index, "", nodeId);
      apply(next);
    } catch (err) { fail(err); }
  };

  return (
    <div
      style={{ position: "fixed", inset: 0, zIndex: 88, background: "rgba(15, 23, 42, 0.36)", display: "flex", alignItems: "center", justifyContent: "center", padding: "24px" }}
      onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}
    >
      <section style={{ width: "min(720px, 100%)", maxHeight: "88dvh", overflow: "hidden", borderRadius: "10px", background: "var(--menu-bg)", border: "1px solid var(--border-color)", boxShadow: "0 24px 60px rgba(15, 23, 42, 0.24)", display: "flex", flexDirection: "column" }}>
        <header style={{ padding: "10px 14px", borderBottom: "1px solid var(--border-color)", display: "flex", alignItems: "center", gap: "10px", flexShrink: 0 }}>
          <input
            value={nameDraft}
            placeholder={task.task_number ? `#${task.task_number} ${t("task.namePlaceholder")}` : t("task.namePlaceholder")}
            onChange={(event) => setNameDraft(event.target.value)}
            onBlur={() => { if (nameDraft.trim() !== (task.name || "")) void saveName(); }}
            style={{ flex: "1 1 auto", minWidth: 0, height: "30px", borderRadius: "6px", border: "1px solid var(--border-color)", background: "transparent", color: "var(--text-color)", padding: "0 8px", fontSize: "14px", fontWeight: 700, outline: "none" }}
          />
          <span style={{ fontSize: "12px", fontWeight: 800, color: statusColors[task.status] || "var(--text-secondary)", flexShrink: 0 }}>
            {statusText(task.status, t)}
          </span>
          <button type="button" onClick={onClose} style={iconBtn}>{t("taskTemplate.close")}</button>
        </header>
        <div style={{ padding: "12px 14px", overflow: "auto", display: "flex", flexDirection: "column", gap: "10px", minHeight: 0 }}>
          {/* 流水 */}
          {stages.map((stage, index) => {
            const run = latestRunFor(index);
            const isCurrent = index === task.current_stage_index;
            const editing = editingStage === index;
            const isAgent = stage.role === "agent";
            return (
              <div key={stage.id || index} style={{ border: isCurrent ? "1px solid var(--accent-color)" : "1px solid var(--border-color)", borderRadius: "8px", background: "var(--panel-bg)", padding: "10px", display: "flex", flexDirection: "column", gap: "8px" }}>
                <div style={{ display: "flex", alignItems: "center", gap: "8px", flexWrap: "wrap" }}>
                  <span style={{ fontWeight: 800, fontSize: "12px", color: isCurrent ? "var(--accent-color)" : "var(--text-color)" }}>
                    {stage.name || t("task.stageLabel", { index: index + 1 })}
                  </span>
                  {isAgent ? (
                    <>
                      <span style={tagStyle}><AgentIcon agentName={stage.agent || "codex"} style={{ width: "12px", height: "12px" }} /> {stage.agent || "codex"}{stage.model ? ` · ${stage.model}` : ""}{stage.effort ? ` · ${stage.effort}` : ""}</span>
                      {stage.auto_advance ? <span style={tagStyle}>{t("taskTemplate.autoAdvance")}</span> : null}
                    </>
                  ) : (
                    <span style={tagStyle}>{t("task.stage.user")}</span>
                  )}
                  {run ? (
                    <span style={{ fontSize: "11px", fontWeight: 700, color: statusColors[run.status] || "var(--text-secondary)" }}>
                      {statusText(run.status, t)}
                    </span>
                  ) : null}
                  <div style={{ marginLeft: "auto", display: "flex", gap: "4px" }}>
                    <button type="button" disabled={editing || terminal} onClick={() => void rerun(index)} title={t("task.rerunStage")} style={{ ...iconBtn, opacity: editing || terminal ? 0.5 : 1 }}>{t("task.rerun")}</button>
                    <button type="button" onClick={() => (editing ? setEditingStage(-1) : startEditStage(index))} title={t("common.edit")} style={iconBtn}>{editing ? "×" : t("common.edit")}</button>
                    {run?.session_key ? (
                      <button type="button" onClick={() => onOpenSession(run.session_key as string)} title={t("task.openSession", { index: 1 })} style={iconBtn}>
                        <AgentIcon agentName="" style={{ width: "14px", height: "14px" }} />
                      </button>
                    ) : null}
                  </div>
                </div>
                {editing ? (
                  <div style={{ display: "flex", flexDirection: "column", gap: "6px" }}>
                    <input value={editName} onChange={(event) => setEditName(event.target.value)} placeholder={t("taskTemplate.stageNamePlaceholder")} style={inputStyle} />
                    {isAgent ? (
                      <div style={{ display: "flex", gap: "8px", alignItems: "center", flexWrap: "wrap" }}>
                        <AgentSelector
                          agent={editAgent}
                          model={editModel}
                          mode={editMode}
                          effort={editEffort}
                          fastService=""
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
                      </div>
                    ) : null}
                    <textarea value={editPrompt} onChange={(event) => setEditPrompt(event.target.value)} rows={3} placeholder={t("taskTemplate.promptTemplate")} style={{ ...inputStyle, height: "auto", padding: "8px", resize: "vertical" }} />
                    <div style={{ display: "flex", justifyContent: "flex-end", gap: "8px" }}>
                      <button type="button" onClick={() => setEditingStage(-1)} style={buttonStyle("secondary")}>{t("taskTemplate.close")}</button>
                      <button type="button" disabled={saving} onClick={() => void saveStage(index)} style={buttonStyle("primary")}>{saving ? t("common.saving") : t("common.save")}</button>
                    </div>
                  </div>
                ) : (
                  <div style={{ fontSize: "12px", lineHeight: "18px", color: "var(--text-color)", whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
                    {run?.rendered_prompt || stage.prompt_template || <span style={{ color: "var(--text-secondary)" }}>—</span>}
                  </div>
                )}
              </div>
            );
          })}
          {stages.length === 0 ? (
            <div style={{ fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.noStages")}</div>
          ) : null}

          {/* 追加 comment 作为下一段 prompt */}
          {!terminal ? (
            <div style={{ border: "1px dashed var(--border-color)", borderRadius: "8px", padding: "10px", display: "flex", flexDirection: "column", gap: "6px", background: "var(--panel-bg)" }}>
              <div style={{ display: "flex", alignItems: "center", gap: "8px", flexWrap: "wrap" }}>
                <span style={{ fontSize: "11px", fontWeight: 800, color: "var(--text-secondary)" }}>{t("task.addComment")}</span>
                <div style={{ height: "26px", borderRadius: "6px", border: "1px solid var(--border-color)", background: "var(--input-bg)", padding: "1px", display: "grid", gridTemplateColumns: "1fr 1fr", marginLeft: "auto" }}>
                  <button type="button" onClick={() => setCommentRole("user")} style={segment(commentRole === "user")}>{t("task.stage.user")}</button>
                  <button type="button" onClick={() => setCommentRole("agent")} style={{ ...segment(commentRole === "agent"), display: "flex", alignItems: "center", justifyContent: "center", gap: "3px" }}>
                    <AgentIcon agentName="codex" style={{ width: "12px", height: "12px", display: "none" }} />
                    agent
                  </button>
                </div>
                {commentRole === "agent" ? (
                  <AgentSelector
                    agent={commentAgent}
                    model={commentModel}
                    mode={commentMode}
                    effort={commentEffort}
                    fastService=""
                    agents={agents}
                    compact
                    menuPlacement="top"
                    showChevron
                    onAgentChange={(agent, model) => {
                      const status = agents.find((item) => item.name === agent) || null;
                      setCommentAgent(agent);
                      setCommentModel(model || "");
                      setCommentMode(status?.current_mode_id || "");
                    }}
                    onModeChange={(mode) => setCommentMode(mode || "")}
                    onEffortChange={(effort) => setCommentEffort(effort || "")}
                    onFastServiceChange={() => {}}
                  />
                ) : null}
              </div>
              <textarea value={commentPrompt} onChange={(event) => setCommentPrompt(event.target.value)} rows={2} placeholder={t("task.commentPlaceholder")} style={{ ...inputStyle, height: "auto", padding: "8px", resize: "vertical" }} />
              <div style={{ display: "flex", justifyContent: "flex-end", gap: "8px" }}>
                <button type="button" onClick={onEditInput} style={buttonStyle("secondary")}>{t("task.editInput")}</button>
                <button type="button" disabled={saving || !commentPrompt.trim()} onClick={() => void submitComment()} style={buttonStyle("primary")}>
                  {saving ? t("common.saving") : commentPrompt.trim() ? t("task.sendComment") : t("task.commentPlaceholder")}
                </button>
              </div>
            </div>
          ) : null}
        </div>
      </section>
    </div>
  );
}

type StageRoleKey = "user" | "agent";

function segment(active: boolean): React.CSSProperties {
  return {
    border: "none",
    borderRadius: "5px",
    background: active ? "var(--accent-color)" : "transparent",
    color: active ? "#fff" : "var(--text-color)",
    fontSize: "11px",
    fontWeight: 800,
    cursor: "pointer",
    padding: "0 10px",
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
