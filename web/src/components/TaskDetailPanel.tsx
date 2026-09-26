import React, { useEffect, useRef, useState } from "react";
import { AgentIcon } from "./AgentIcon";
import { ModeIcon } from "./ModeIcon";
import { PromptEditor } from "./PromptEditor";
import { StageEditor } from "./StageEditor";
import { PanelShell, panelButtonStyle, panelIconButtonStyle } from "./PanelShell";
import type { SessionReusePolicy } from "./StageOptionsBar";
import { PencilIcon, TrashIcon, composerInputStyle } from "./action/composerStyles";
import { uploadFiles } from "../services/upload";
import { useI18n, type I18nContextValue } from "../i18n";
import {
  addTaskStage,
  removeTaskStage,
  renameTask,
  updateTaskStage,
  type StageTemplate,
  type TaskDetail,
} from "../services/tasks";
import type { AgentStatus } from "../services/agents";
import { reportError } from "../services/error";
import { DEFAULT_TASK_AGENT, DEFAULT_TASK_MODEL, inheritAgentStage } from "../app/appTask";

export type TaskDetailPanelProps = {
  detail: TaskDetail | null;
  agents: AgentStatus[];
  onClose: () => void;
  onOpenSession: (sessionKey: string) => void;
  onMoved?: (detail: TaskDetail) => void;
  nodeId?: string;
  /** 节点主题色，用于发送/编辑按钮 */
  accentColor?: string;
};

const statusColors: Record<string, string> = {
  pending: "var(--text-secondary)",
  running: "var(--accent-color)",
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

function latestStageRun(detail: TaskDetail, index: number): TaskDetail["stage_runs"][number] | null {
  return (detail.stage_runs || [])
    .filter((run) => run.stage_index === index)
    .sort((a, b) => (a.updated_at < b.updated_at ? 1 : -1))[0] || null;
}

export function TaskDetailPanel({ detail, agents, onClose, onOpenSession, onMoved, nodeId, accentColor }: TaskDetailPanelProps) {
  const { t } = useI18n();
  const task = detail?.task || null;

  // 任务改名：文本 ↔ 编辑态，确认后提交
  const [nameDraft, setNameDraft] = useState("");
  const [editingName, setEditingName] = useState(false);
  // 阶段编辑态：名字 + prompt + 模型 一次保存
  const [editingStage, setEditingStage] = useState(-1);
  const [editName, setEditName] = useState("");
  const [editPrompt, setEditPrompt] = useState("");
  const [editAgent, setEditAgent] = useState(DEFAULT_TASK_AGENT);
  const [editModel, setEditModel] = useState("");
  const [editEffort, setEditEffort] = useState("");
  const [editMode, setEditMode] = useState("");
  // 阶段选项：与任务模板编辑共用 StageOptionsBar 那一套
  const [editAutoAdvance, setEditAutoAdvance] = useState(false);
  // 首段（任务输入）专用：「立即执行」= 建完就开跑
  const [editStartImmediately, setEditStartImmediately] = useState(false);
  const [editPlanMode, setEditPlanMode] = useState(false);
  const [editSessionReuse, setEditSessionReuse] = useState<SessionReusePolicy>("task_main");
  // 角色也可在编辑态切换（agent ↔ user），首段固定 user
  const [editRole, setEditRole] = useState<"user" | "agent">("agent");
  const [saving, setSaving] = useState(false);
  const stageAttachRef = useRef<HTMLInputElement | null>(null);
  // 待确认动作：切换编辑对象会丢草稿 / 删除阶段。共用一个居中确认弹窗。
  const [pendingConfirm, setPendingConfirm] = useState<
    { type: "discard"; index: number } | { type: "remove"; index: number } | null
  >(null);

  useEffect(() => {
    setNameDraft(task?.name || "");
    setEditingName(false);
    setEditingStage(-1);
    setPendingConfirm(null);
  }, [task?.id]);

  useEffect(() => {
    if (!editingName) setNameDraft(task?.name || "");
  }, [task?.name, editingName]);

  // Esc：先关确认弹窗，再退出阶段编辑
  useEffect(() => {
    if (pendingConfirm) {
      const onEsc = (event: KeyboardEvent) => {
        if (event.key === "Escape") { event.preventDefault(); setPendingConfirm(null); }
      };
      window.addEventListener("keydown", onEsc);
      return () => window.removeEventListener("keydown", onEsc);
    }
    if (editingStage < 0) return;
    const onEsc = (event: KeyboardEvent) => {
      if (event.key === "Escape") { event.preventDefault(); setEditingStage(-1); }
    };
    window.addEventListener("keydown", onEsc);
    return () => window.removeEventListener("keydown", onEsc);
  }, [editingStage, pendingConfirm]);

  if (!task || !detail) return null;

  const stages = task.stages || [];

  // 第一段 user 段 = 任务输入
  const initialInput = (latestStageRun(detail, 0)?.input || stages[0]?.prompt_template || "").trim();
  const bodyStages = stages.slice(1); // 阶段流从 index 1 起

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
    // 已执行过的段只可看：任何入口都不该把它拉进编辑态。
    const existingRun = latestStageRun(detail, index);
    if (existingRun && String(existingRun.status) !== "pending") return;
    setEditingStage(index);
    setEditName(stage.name || "");
    setEditPrompt(stage.prompt_template || "");
    setEditAgent(stage.agent || DEFAULT_TASK_AGENT);
    setEditModel(stage.model || "");
    setEditEffort(stage.effort || "");
    setEditMode(stage.mode || "");
    setEditAutoAdvance(stage.auto_advance === true);
    setEditStartImmediately(stage.start_immediately === true);
    setEditPlanMode(stage.plan_mode === true);
    setEditSessionReuse((stage.session_reuse_policy as SessionReusePolicy) || "task_main");
    setEditRole(stage.role);
  };

  // 编辑态是否已有未保存修改
  const hasStageDraftChanges = (index: number): boolean => {
    const stage = stages[index];
    if (!stage || editingStage !== index) return false;
    return (
      editName !== (stage.name || "")
      || editPrompt !== (stage.prompt_template || "")
      || editAgent !== (stage.agent || DEFAULT_TASK_AGENT)
      || editModel !== (stage.model || "")
      || editEffort !== (stage.effort || "")
      || editMode !== (stage.mode || "")
      || editAutoAdvance !== (stage.auto_advance === true)
      || editStartImmediately !== (stage.start_immediately === true)
      || editPlanMode !== (stage.plan_mode === true)
      || editSessionReuse !== ((stage.session_reuse_policy as SessionReusePolicy) || "task_main")
    );
  };

  const cancelEditStage = () => {
    setEditingStage(-1);
    setPendingConfirm(null);
  };

  // 点铅笔：有草稿改到别的段时先确认
  const requestEditStage = (index: number) => {
    if (editingStage === index) return;
    if (hasStageDraftChanges(editingStage)) {
      setPendingConfirm({ type: "discard", index });
      return;
    }
    startEditStage(index);
  };

  // 点垃圾桶：删除未执行阶段，先确认
  const requestRemoveStage = (index: number) => {
    setPendingConfirm({ type: "remove", index });
  };

  const removeStage = async (index: number) => {
    try {
      setSaving(true);
      apply(await removeTaskStage(task.root_id, task.id, index, nodeId));
    } catch (err) { fail(err); } finally { setSaving(false); }
  };

  const saveStage = async (index: number) => {
    const original = stages[index];
    if (!original || !editPrompt.trim()) return;
    // 已执行过的阶段只可看：UI 上铅笔已禁，这里再兜一道 ——
    // 就算将来别的入口把 editingStage 指到已执行的段，也不会把它改写掉。
    const run = latestStageRun(detail, index);
    if (run && String(run.status) !== "pending") return;
    const isAgent = editRole === "agent";
    const nextStage: StageTemplate = {
      ...original,
      name: editName,
      role: editRole,
      prompt_template: editPrompt,
      agent: isAgent ? editAgent : undefined,
      model: isAgent ? editModel : undefined,
      effort: isAgent ? editEffort : undefined,
      mode: isAgent ? editMode : undefined,
      auto_advance: editAutoAdvance,
      start_immediately: editStartImmediately,
      plan_mode: isAgent ? editPlanMode : false,
      session_reuse_policy: editSessionReuse,
    };
    try {
      setSaving(true);
      apply(await updateTaskStage(task.root_id, task.id, index, nextStage, nodeId));
      setEditingStage(-1);
    } catch (err) { fail(err); } finally { setSaving(false); }
  };

  const handleStageAttach = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files || []);
    event.currentTarget.value = "";
    if (files.length === 0 || !task.root_id) return;
    try {
      setSaving(true);
      const uploaded = await uploadFiles({ rootId: task.root_id, files, nodeId });
      const tokens = uploaded.map((file) => `[file: ${file.agent_path || file.path}]`).join("\n");
      if (tokens) setEditPrompt((prev) => [prev.trim(), tokens].filter(Boolean).join("\n"));
    } catch (err) { fail(err); } finally { setSaving(false); }
  };

  const appendStage = async () => {
    // 复制紧挨着的上一段（agent/model/选项），省得从头点；新段自带草稿 → 直接进编辑态。
    const stage = inheritAgentStage(stages, stages.length);
    setEditName(stage.name || "");
    setEditPrompt(stage.prompt_template || "");
    setEditAgent(stage.agent || DEFAULT_TASK_AGENT);
    setEditModel(stage.model || DEFAULT_TASK_MODEL);
    setEditEffort(stage.effort || "");
    setEditMode(stage.mode || "");
    setEditAutoAdvance(stage.auto_advance === true);
    setEditStartImmediately(stage.start_immediately === true);
    setEditPlanMode(stage.plan_mode === true);
    setEditSessionReuse((stage.session_reuse_policy as SessionReusePolicy) || "task_main");
    setEditRole("agent");
    try {
      setSaving(true);
      const next = await addTaskStage(task.root_id, task.id, stage, nodeId);
      apply(next);
      setEditingStage((next.task.stages?.length || 1) - 1);
    } catch (err) { fail(err); } finally { setSaving(false); }
  };

  const numberLabel = task.task_number ? `#${task.task_number}` : "";

  return (
    <>
    <PanelShell
      width={720}
      onClose={onClose}
      closeOnOverlayClick
      title={(
        <span style={{ flex: "1 1 auto", minWidth: 0, fontSize: "14px", fontWeight: 700, color: "var(--text-color)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {task.name || t("task.namePlaceholder")}
        </span>
      )}
      headerLeft={editingName ? (
        <input
          autoFocus
          value={nameDraft}
          placeholder={t("task.namePlaceholder")}
          onChange={(event) => setNameDraft(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") void saveName();
            if (event.key === "Escape") { setEditingName(false); setNameDraft(task.name || ""); }
          }}
          style={{ ...composerInputStyle, flex: "1 1 auto", height: "30px", border: "1px solid var(--accent-color)", fontWeight: 700 }}
        />
      ) : (
        <>
          <span style={{ fontSize: "12px", fontWeight: 800, color: statusColors[task.status] || "var(--text-secondary)", flexShrink: 0, whiteSpace: "nowrap" }}>
            {statusText(task.status, t)}
          </span>
          <span style={{ fontSize: "12px", fontWeight: 700, color: "var(--text-secondary)", flexShrink: 0, whiteSpace: "nowrap" }}>
            {task.task_template_name || ""}
          </span>
          {numberLabel ? (
            <span style={{ fontSize: "12px", fontWeight: 800, color: "#0ea5e9", flexShrink: 0, whiteSpace: "nowrap" }}>{numberLabel}</span>
          ) : null}
        </>
      )}
      headerRight={editingName ? (
        <>
          <button type="button" disabled={saving} onClick={() => void saveName()} style={panelButtonStyle("primary")}>{t("common.confirm")}</button>
          <button type="button" onClick={() => { setEditingName(false); setNameDraft(task.name || ""); }} style={panelButtonStyle("secondary")}>{t("common.cancel")}</button>
        </>
      ) : (
        <button type="button" aria-label={t("task.renameTask")} title={t("task.renameTask")} onClick={() => setEditingName(true)} style={pencilStyle(false)}>
          <PencilIcon />
        </button>
      )}
    >
      <div style={{ padding: "12px 14px", display: "flex", flexDirection: "column", gap: "10px" }}>
          {initialInput ? (
            <div style={{ border: "1px dashed var(--border-color)", borderRadius: "8px", padding: "10px", display: "flex", flexDirection: "column", gap: "6px", background: "var(--panel-bg)" }}>
              <span style={{ fontSize: "11px", fontWeight: 800, color: "var(--text-secondary)" }}>{t("task.initialInput")}</span>
              <PromptEditor
                value={initialInput}
                mode="readonly"
                role="user"
                resetKey={task.id}
                placeholder={t("task.namePlaceholder")}
              />
            </div>
          ) : null}

          {bodyStages.map((stage, i) => {
            const index = i + 1; // 原始 stage index
            const run = latestStageRun(detail, index);
            const isCurrent = index === task.current_stage_index;
            const executed = !!run && String(run.status) !== "pending";
            const editing = editingStage === index;
            const isAgent = stage.role === "agent";
            const shown = executed ? (run?.rendered_prompt || stage.prompt_template || "") : (stage.prompt_template || "");
            // 编辑态用草稿值渲染，未保存前先让用户看到自己刚改的
            const displayStage: StageTemplate = editing
              ? {
                  ...stage,
                  name: editName,
                  role: editRole,
                  prompt_template: editPrompt,
                  agent: editRole === "agent" ? editAgent : undefined,
                  model: editRole === "agent" ? editModel : undefined,
                  effort: editRole === "agent" ? editEffort : undefined,
                  mode: editRole === "agent" ? editMode : undefined,
                  auto_advance: editAutoAdvance,
                  start_immediately: editStartImmediately,
                  plan_mode: editRole === "agent" ? editPlanMode : false,
                  session_reuse_policy: editSessionReuse,
                }
              : stage;
            return (
              <div key={stage.id || index} style={{ border: isCurrent ? "1px solid var(--accent-color)" : "1px solid var(--border-color)", borderRadius: "8px", background: "var(--panel-bg)", padding: "10px", display: "flex", flexDirection: "column", gap: "8px" }}>
                <div style={{ display: "flex", alignItems: "center", gap: "8px", flexWrap: "wrap" }}>
                  {!editing ? (
                    <span style={{ fontWeight: 800, fontSize: "12px", color: isCurrent ? "var(--accent-color)" : "var(--text-color)" }}>
                      {stage.name || t("task.stageLabel", { index })}
                    </span>
                  ) : null}
                  {!isAgent && !editing ? (
                    <span style={tagStyle}>{t("task.stage.user")}</span>
                  ) : null}
                  <span style={{ fontSize: "11px", fontWeight: 700, color: statusColors[run?.status || ""] || "var(--text-secondary)" }}>
                    {executed ? statusText(run.status, t) : t("task.stage.notExecuted")}
                  </span>
                  {run?.session_key ? (
                    <button
                      type="button"
                      title={t("task.openSession", { index: 1 })}
                      aria-label={t("task.openSession", { index: 1 })}
                      onClick={() => onOpenSession(run.session_key as string)}
                      style={sessionIconButtonStyle}
                    >
                      <span style={{ position: "relative", width: "18px", height: "18px", display: "inline-flex", alignItems: "center", justifyContent: "center" }}>
                        <ModeIcon type="task" size={16} />
                        <span style={{ position: "absolute", right: "-2px", bottom: "-2px", width: "10px", height: "10px", borderRadius: "999px", background: "var(--content-bg, #fff)", border: "1px solid rgba(255,255,255,0.9)", display: "flex", alignItems: "center", justifyContent: "center", overflow: "hidden" }}>
                          <AgentIcon agentName={stage.agent || ""} style={{ width: "10px", height: "10px", display: "block" }} />
                        </span>
                      </span>
                    </button>
                  ) : null}
                  {editing ? (
                    <button type="button" onClick={cancelEditStage} style={{ ...panelButtonStyle("secondary"), marginLeft: "auto", height: "26px", fontSize: "11px" }}>
                      {t("common.cancel")}
                    </button>
                  ) : null}
                  {!executed && !isCurrent ? (
                    <button
                      type="button"
                      title={t("task.removeStage")}
                      aria-label={t("task.removeStage")}
                      disabled={saving}
                      onMouseDown={(event) => event.preventDefault()}
                      onClick={() => requestRemoveStage(index)}
                      style={{ ...panelIconButtonStyle(true, saving), marginLeft: editing ? 0 : "auto" }}
                    >
                      <TrashIcon />
                    </button>
                  ) : null}
                </div>

                <StageEditor
                  stage={displayStage}
                  agents={agents}
                  onChange={(patch: Partial<StageTemplate>) => {
                    if ("name" in patch) setEditName(patch.name || "");
                    if ("role" in patch) setEditRole(patch.role as "user" | "agent");
                    if ("prompt_template" in patch) setEditPrompt(patch.prompt_template || "");
                    if ("agent" in patch) setEditAgent(patch.agent || DEFAULT_TASK_AGENT);
                    if ("model" in patch) setEditModel(patch.model || "");
                    if ("effort" in patch) setEditEffort(patch.effort || "");
                    if ("mode" in patch) setEditMode(patch.mode || "");
                    if ("auto_advance" in patch) setEditAutoAdvance(patch.auto_advance === true);
                    if ("start_immediately" in patch) setEditStartImmediately(patch.start_immediately === true);
                    if ("plan_mode" in patch) setEditPlanMode(patch.plan_mode === true);
                    if ("session_reuse_policy" in patch) setEditSessionReuse((patch.session_reuse_policy as SessionReusePolicy) || "task_main");
                  }}
                  stageNamePlaceholder={editing ? t("taskTemplate.stageNamePlaceholder") : undefined}
                  isFirstStage={index === 0}
                  onStageNameChange={(name) => setEditName(name)}
                  onToggleRole={editing ? () => {
                    if (editRole === "agent") {
                      setEditRole("user");
                      setEditAgent(DEFAULT_TASK_AGENT);
                      setEditModel("");
                      setEditEffort("");
                      setEditMode("");
                      setEditPlanMode(false);
                    } else {
                      setEditRole("agent");
                    }
                  } : undefined}
                  resetKey={`${task.id}-${index}`}
                  mode={editing ? "editable" : (executed ? "done" : "readonly")}
                  /* 已执行过的段只可看：不给 onEdit，PromptEditor 的铅笔就是禁的 */
                  onEdit={editing || executed ? undefined : () => requestEditStage(index)}
                  onSend={editing && !executed ? () => void saveStage(index) : undefined}
                  onAttach={editing ? () => stageAttachRef.current?.click() : undefined}
                  sending={saving}
                  sendDisabled={!editPrompt.trim()}
                  placeholder={t("taskTemplate.promptTemplate")}
                  accentColor={accentColor}
                  displayValue={editing ? editPrompt : shown}
                />
              </div>
            );
          })}
          {bodyStages.length === 0 ? (
            <div style={{ fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.noStages")}</div>
          ) : null}

          <button type="button" disabled={saving} onClick={() => void appendStage()} style={{ ...panelButtonStyle("secondary"), alignSelf: "flex-start" }}>
            {t("task.appendStage")}
          </button>
          <input ref={stageAttachRef} type="file" multiple style={{ display: "none" }} onChange={(event) => void handleStageAttach(event)} />
        </div>
    </PanelShell>
      {pendingConfirm ? (
        <div
          style={{ position: "fixed", inset: 0, zIndex: 96, background: "rgba(15, 23, 42, 0.28)", display: "flex", alignItems: "center", justifyContent: "center", padding: "24px" }}
          onMouseDown={(event) => { if (event.target === event.currentTarget) setPendingConfirm(null); }}
        >
          <section style={{ width: "min(420px, 100%)", borderRadius: "10px", border: "1px solid var(--border-color)", background: "var(--menu-bg)", boxShadow: "0 24px 60px rgba(15, 23, 42, 0.24)", overflow: "hidden" }}>
            <div style={{ padding: "12px 14px", borderBottom: "1px solid var(--border-color)", display: "flex", alignItems: "center", gap: "10px" }}>
              <div style={{ minWidth: 0, fontSize: "13px", fontWeight: 800, color: "var(--text-color)" }}>
                {pendingConfirm.type === "discard" ? t("task.discardDraftTitle") : t("task.removeStageTitle")}
              </div>
            </div>
            <div style={{ padding: "14px", display: "flex", flexDirection: "column", gap: "12px" }}>
              <div style={{ fontSize: "12px", lineHeight: 1.5, color: "var(--text-secondary)" }}>
                {pendingConfirm.type === "discard" ? t("task.discardDraftMessage") : t("task.removeStageMessage")}
              </div>
              <div style={{ display: "flex", justifyContent: "flex-end", gap: "8px" }}>
                <button type="button" onClick={() => setPendingConfirm(null)} style={panelButtonStyle("secondary")}>
                  {pendingConfirm.type === "discard" ? t("task.keepEditing") : t("common.cancel")}
                </button>
                <button
                  type="button"
                  disabled={saving}
                  onClick={() => {
                    const pending = pendingConfirm;
                    setPendingConfirm(null);
                    if (pending.type === "discard") startEditStage(pending.index);
                    else {
                      setEditingStage(-1);
                      void removeStage(pending.index);
                    }
                  }}
                  style={panelButtonStyle("danger")}
                >
                  {pendingConfirm.type === "discard" ? t("task.discardDraft") : t("common.delete")}
                </button>
              </div>
            </div>
          </section>
        </div>
      ) : null}
    </>
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

const sessionIconButtonStyle: React.CSSProperties = {
  width: "22px",
  height: "22px",
  border: "none",
  borderRadius: "6px",
  background: "transparent",
  color: "var(--text-secondary)",
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  cursor: "pointer",
  padding: 0,
  marginLeft: "auto",
};

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

