import { useEffect, useRef, useState } from "react";
import { PromptEditor } from "../editor/PromptEditor";
import type { SessionReusePolicy } from "./StageOptionsBar";
import { fileTokenPath, formatFileToken, uploadFiles } from "../../services/upload";
import { statusText, latestStageRun } from "./taskDetailUtils";
import { confirmDialog } from "../../services/dialog";
import {
  addTaskStage,
  beginTaskFinishWorktree,
  deleteTask,
  moveTask,
  removeTaskStage,
  renameTask,
  updateTaskStage,
  type StageTemplate,
  type TaskDetail,
} from "../../services/tasks";
import { reportError } from "../../services/net/error";
import { canAdvanceFromCurrentStage, isFinishStageActive, isTerminalKanbanTask, nextRunnableStageIndex } from "../../app/appTask";
import { DEFAULT_TASK_AGENT, DEFAULT_TASK_MODEL, inheritAgentStage } from "../../app/appTask";

export function useTaskDetailPanel({
  detail,
  agents,
  nodeId,
  onClose,
  onMoved,
  onRunTask,
  t,
}: {
  detail: TaskDetail | null;
  agents: any[];
  nodeId?: string;
  onClose: () => void;
  onMoved?: (detail: TaskDetail) => void;
  onRunTask?: (task: any) => void | Promise<void>;
  t: (key: any, fallback?: any) => string;
}) {
  const task = detail?.task || null;

  const [nameDraft, setNameDraft] = useState("");
  const [editingName, setEditingName] = useState(false);
  const [editingStage, setEditingStage] = useState(-1);
  const [editName, setEditName] = useState("");
  const [editPrompt, setEditPrompt] = useState("");
  const [editAgent, setEditAgent] = useState(DEFAULT_TASK_AGENT);
  const [editModel, setEditModel] = useState("");
  const [editEffort, setEditEffort] = useState("");
  const [editMode, setEditMode] = useState("");
  const [editAutoAdvance, setEditAutoAdvance] = useState(false);
  const [editStartImmediately, setEditStartImmediately] = useState(false);
  const [editPlanMode, setEditPlanMode] = useState(false);
  const [editSessionReuse, setEditSessionReuse] = useState<SessionReusePolicy>("task_main");
  const [editRole, setEditRole] = useState<"user" | "agent">("agent");
  const [saving, setSaving] = useState(false);
  const stageAttachRef = useRef<HTMLInputElement | null>(null);
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
  const initialInput = (latestStageRun(detail, 0)?.input || stages[0]?.prompt_template || "").trim();
  const bodyStages = stages.slice(1);
  const runnableStageIndex = nextRunnableStageIndex(detail, task.current_stage_index);
  const advanceable = canAdvanceFromCurrentStage(detail, task.current_stage_index);
  const finishActive = isFinishStageActive(task);
  const hasAgentStage = (task?.stages || []).some((stage) => stage.role === "agent");
  const canFinishWorktree = task?.create_worktree === true
    && !!task?.worktree_path
    && task?.worktree_missing !== true
    && hasAgentStage;
  const terminal = isTerminalKanbanTask(task);
  const stageRunningNow = task.current_stage_status === "running" && task.status === "running";
  const canRunStageNow = runnableStageIndex >= 0 && advanceable;
  const canCompleteTask = !terminal && !stageRunningNow && !canRunStageNow && !(finishActive && canFinishWorktree);
  const numberLabel = task.task_number ? `#${task.task_number}` : "";

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

  const requestEditStage = (index: number) => {
    if (editingStage === index) return;
    if (hasStageDraftChanges(editingStage)) {
      setPendingConfirm({ type: "discard", index });
      return;
    }
    startEditStage(index);
  };

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
      const tokens = uploaded.map((file) => formatFileToken(fileTokenPath(file))).join("\n");
      if (tokens) setEditPrompt((prev) => [prev.trim(), tokens].filter(Boolean).join("\n"));
    } catch (err) { fail(err); } finally { setSaving(false); }
  };

  const appendStage = async () => {
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

  const runStage = async () => {
    if (!onRunTask) return;
    try {
      setSaving(true);
      await onRunTask(task);
    } finally { setSaving(false); }
  };

  const finishWorktree = async () => {
    if (!task) return;
    if (!await confirmDialog({ message: t("task.finishWorktreeConfirm"), confirmLabel: t("task.finishWorktree"), danger: true })) {
      return;
    }
    try {
      setSaving(true);
      const res = await beginTaskFinishWorktree(task.root_id, task.id, nodeId);
      if (res.detail) {
        apply(res.detail);
      }
      const message = res.action === "session_running"
        ? t("task.finishWorktreeSessionRunning")
        : res.action === "nudged"
          ? t("task.finishWorktreeNudged")
          : t("task.finishWorktreeStarted");
      reportError("file.write_failed", message, { severity: "info", recoverable: false });
    } catch (error) {
      reportError("file.write_failed", String((error as Error)?.message || t("task.actionFailed")));
    } finally { setSaving(false); }
  };

  const completeTask = async () => {
    if (!task) return;
    try {
      setSaving(true);
      apply(await moveTask(task.root_id, task.id, "complete", "", nodeId));
    } catch (error) {
      reportError("file.write_failed", String((error as Error)?.message || t("task.actionFailed")));
    } finally { setSaving(false); }
  };

  const deleteTaskCard = async () => {
    if (!task) return;
    if (!await confirmDialog({ message: t("task.deleteTaskConfirm"), confirmLabel: t("common.delete"), danger: true })) {
      return;
    }
    try {
      setSaving(true);
      await deleteTask(task.root_id, task.id, nodeId);
      onClose();
    } catch (error) {
      reportError("file.write_failed", String((error as Error)?.message || t("task.actionFailed")));
    } finally { setSaving(false); }
  };

  return {
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
  };
}
