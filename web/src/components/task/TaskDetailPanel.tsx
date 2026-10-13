import React from "react";
import { AgentIcon } from "../agent/AgentIcon";
import { ModeIcon } from "../agent/ModeIcon";
import { PromptEditor } from "../editor/PromptEditor";
import { StageEditor } from "./StageEditor";
import { PanelShell, panelButtonStyle, panelIconButtonStyle } from "../shell/PanelShell";
import type { SessionReusePolicy } from "./StageOptionsBar";
import { PencilIcon, TrashIcon } from "../action/composerIcons";
import { composerInputStyle } from "../action/composerStyles";
import { useI18n } from "../../i18n/index";
import { statusText, latestStageRun } from "./taskDetailUtils";
import { pencilStyle, sessionIconButtonStyle, runIconButtonStyle, tagStyle } from "./taskDetailStyles";
import { TaskDetailConfirmDialog } from "./TaskDetailConfirmDialog";
import type { KanbanTask, StageTemplate, TaskDetail } from "../../services/tasks";
import type { AgentStatus } from "../../services/agents";
import { taskStatusColor, isTerminalKanbanTask } from "../../app/appTask";
import { DEFAULT_TASK_AGENT } from "../../app/appTask";
import { DeleteIcon, RunNowIcon, TaskCompleteIcon, TaskFinishWorktreeIcon } from "../../app/taskIcons";
import { useTaskDetailPanel } from "./useTaskDetailPanel";

export type TaskDetailPanelProps = {
  detail: TaskDetail | null;
  agents: AgentStatus[];
  onClose: () => void;
  onOpenSession: (sessionKey: string) => void;
  onMoved?: (detail: TaskDetail) => void;
  onRunTask?: (task: KanbanTask) => void | Promise<void>;
  nodeId?: string;
  accentColor?: string;
};

export function TaskDetailPanel({ detail, agents, onClose, onOpenSession, onMoved, onRunTask, nodeId, accentColor }: TaskDetailPanelProps) {
  const { t } = useI18n();
  const panel = useTaskDetailPanel({ detail, agents, nodeId, onClose, onMoved, onRunTask, t: t as (key: any, fallback?: any) => string });
  if (!panel) return null;
  const {
    task,
    stages,
    initialInput,
    bodyStages,
    editingName,
    setEditingName,
    nameDraft,
    setNameDraft,
    editingStage,
    setEditingStage,
    editName,
    setEditName,
    editPrompt,
    setEditPrompt,
    editAgent,
    setEditAgent,
    editModel,
    setEditModel,
    editEffort,
    setEditEffort,
    editMode,
    setEditMode,
    editAutoAdvance,
    setEditAutoAdvance,
    editStartImmediately,
    setEditStartImmediately,
    editPlanMode,
    setEditPlanMode,
    editSessionReuse,
    setEditSessionReuse,
    editRole,
    setEditRole,
    saving,
    pendingConfirm,
    setPendingConfirm,
    stageAttachRef,
    finishActive,
    canFinishWorktree,
    terminal,
    canCompleteTask,
    canRunStageNow,
    runnableStageIndex,
    advanceable,
    numberLabel,
    saveName,
    startEditStage,
    cancelEditStage,
    requestEditStage,
    requestRemoveStage,
    removeStage,
    saveStage,
    handleStageAttach,
    appendStage,
    runStage,
    finishWorktree,
    completeTask,
    deleteTaskCard,
  } = panel;

  return (
    <>
    <PanelShell
      width={720}
      onClose={onClose}
      hasUnsavedChanges={() => editingName && nameDraft.trim() !== (task.name || "").trim()}
      title={editingName ? (
        <input
          autoFocus
          value={nameDraft}
          placeholder={t("task.namePlaceholder")}
          onChange={(event) => setNameDraft(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") void saveName();
            if (event.key === "Escape") { setEditingName(false); setNameDraft(task.name || ""); }
          }}
          style={{ ...composerInputStyle, flex: "1 1 auto", minWidth: 0, height: "30px", border: "1px solid var(--accent-color)", fontWeight: 700 }}
        />
      ) : (
        <span style={{ flex: "1 1 auto", minWidth: 0, fontSize: "14px", fontWeight: 700, color: "var(--text-color)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {task.name || t("task.namePlaceholder")}
        </span>
      )}
      headerLeft={(
        <>
          <span style={{ fontSize: "12px", fontWeight: 800, color: taskStatusColor(task.status), flexShrink: 0, whiteSpace: "nowrap" }}>
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
        <>
          {canFinishWorktree ? (
            <button
              type="button"
              title={t("task.finishWorktree")}
              aria-label={t("task.finishWorktree")}
              disabled={saving}
              onClick={() => void finishWorktree()}
              style={{ ...pencilStyle(false), color: "var(--status-ok)", opacity: saving ? 0.4 : 1 }}
            >
              <TaskFinishWorktreeIcon />
            </button>
          ) : null}
          {canCompleteTask ? (
            <button
              type="button"
              title={t("task.completeShort")}
              aria-label={t("task.complete")}
              disabled={saving}
              onClick={() => void completeTask()}
              style={{ ...pencilStyle(false), color: "var(--status-ok)", opacity: saving ? 0.4 : 1 }}
            >
              <TaskCompleteIcon />
            </button>
          ) : null}
          {terminal ? (
            <button
              type="button"
              title={t("task.deleteTask")}
              aria-label={t("task.deleteTask")}
              disabled={saving}
              onClick={() => void deleteTaskCard()}
              style={{ ...pencilStyle(false), color: "#dc2626", opacity: saving ? 0.4 : 1 }}
            >
              <DeleteIcon />
            </button>
          ) : null}
          <button type="button" aria-label={t("task.renameTask")} title={t("task.renameTask")} onClick={() => setEditingName(true)} style={pencilStyle(false)}>
            <PencilIcon />
          </button>
        </>
      )}
    >
      <div style={{ padding: "12px 14px", display: "flex", flexDirection: "column", gap: "10px" }}>
          {/* 收尾进行中不再挂转圈横幅（2026-10-07 用户要求）：卡片上那个绿色 worktree
              标签会变成「收尾中」，那才是唯一的进度信号。收尾段成功/受阻之后清场由服务端
              自己接着跑，结论走 WS 的 task.finish_teardown 回到 App 那个弹窗 / toast。 */}
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
            const index = i + 1;
            const run = latestStageRun(detail!, index);
            const isCurrent = index === task.current_stage_index;
            const executed = !!run && String(run.status) !== "pending";
            const editing = editingStage === index;
            const isAgent = stage.role === "agent";
            const canRunStage = index === runnableStageIndex
              && advanceable
              && !editing
              && !!onRunTask
              && !isTerminalKanbanTask(task)
              && !finishActive
              && !(task.current_stage_status === "running" && task.status === "running");
            const shown = executed ? (run?.rendered_prompt || stage.prompt_template || "") : (stage.prompt_template || "");
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
                  <div style={{ display: "flex", alignItems: "center", gap: "8px", flexWrap: "wrap", flex: "1 1 auto", minWidth: 0 }}>
                    {!editing ? (
                      <span style={{ fontWeight: 800, fontSize: "12px", color: isCurrent ? "var(--accent-color)" : "var(--text-color)" }}>
                        {stage.name || t("task.stageLabel", { index })}
                      </span>
                    ) : null}
                    {!isAgent && !editing ? (
                      <span style={tagStyle}>{t("task.stage.user")}</span>
                    ) : null}
                    <span style={{ fontSize: "11px", fontWeight: 700, color: taskStatusColor(run?.status || "") }}>
                      {executed ? statusText(run.status, t) : t("task.stage.notExecuted")}
                    </span>
                  </div>
                  <div style={{ display: "flex", alignItems: "center", gap: "2px", flexShrink: 0 }}>
                    {canRunStage ? (
                      <button
                        type="button"
                        title={t("task.runNow")}
                        aria-label={t("task.runNow")}
                        disabled={saving}
                        onMouseDown={(event) => event.preventDefault()}
                        onClick={() => void runStage()}
                        style={{ ...runIconButtonStyle, opacity: saving ? 0.4 : 1 }}
                      >
                        <RunNowIcon />
                      </button>
                    ) : null}
                    {run?.session_key ? (
                      <button
                        type="button"
                        title={t("task.openSession", { index: 1 })}
                        aria-label={t("task.openSession", { index: 1 })}
                        onClick={() => onOpenSession(run.session_key as string)}
                        style={{ ...sessionIconButtonStyle, marginLeft: 0 }}
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
                      <button type="button" onClick={cancelEditStage} style={{ ...panelButtonStyle("secondary"), height: "26px", fontSize: "11px" }}>
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
                        style={{ ...panelIconButtonStyle(true, saving), marginLeft: "auto" }}
                      >
                        <TrashIcon />
                      </button>
                    ) : null}
                  </div>
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
      <TaskDetailConfirmDialog
        pendingConfirm={pendingConfirm}
        saving={saving}
        onCancel={() => setPendingConfirm(null)}
        onConfirm={() => {
          const pending = pendingConfirm;
          setPendingConfirm(null);
          if (!pending) return;
          if (pending.type === "discard") startEditStage(pending.index);
          else {
            setEditingStage(-1);
            void removeStage(pending.index);
          }
        }}
      />
    </>
  );
}
