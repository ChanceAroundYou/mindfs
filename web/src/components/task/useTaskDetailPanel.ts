import { useEffect, useState } from "react";
import { confirmDialog } from "../../services/dialog";
import {
  beginTaskFinishWorktree,
  deleteTask,
  moveTask,
  renameTask,
  type TaskDetail,
} from "../../services/tasks";
import { reportError } from "../../services/net/error";
import { isTerminalKanbanTask } from "../../app/appTask";

/**
 * 任务详情面板的头部动作（改名 / 收尾 / 完成 / 删除）。
 *
 * 对话式模型下详情面板只负责头部操作与外壳；内容由 ConversationView 自己取数。
 * 阶段编辑已随阶段模型整体移除 —— 历史数据在迁移时转成 activities 与
 * WorkflowRun，不再有可编辑的阶段列表。
 */
export function useTaskDetailPanel({
  detail,
  nodeId,
  onClose,
  onMoved,
  t,
}: {
  detail: TaskDetail | null;
  nodeId?: string;
  onClose: () => void;
  onMoved?: (detail: TaskDetail) => void;
  t: (key: any, fallback?: any) => string;
}) {
  const task = detail?.task || null;

  const [nameDraft, setNameDraft] = useState("");
  const [editingName, setEditingName] = useState(false);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setNameDraft(task?.name || "");
    setEditingName(false);
  }, [task?.id]);

  useEffect(() => {
    if (!editingName) setNameDraft(task?.name || "");
  }, [task?.name, editingName]);

  useEffect(() => {
    if (!editingName) return;
    const onEsc = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        setEditingName(false);
        setNameDraft(task?.name || "");
      }
    };
    window.addEventListener("keydown", onEsc);
    return () => window.removeEventListener("keydown", onEsc);
  }, [editingName, task?.name]);

  if (!task || !detail) return null;

  const canFinishWorktree = task?.create_worktree === true
    && !!task?.worktree_path
    && task?.worktree_missing !== true;
  const terminal = isTerminalKanbanTask(task);
  const taskRunningNow = task.current_stage_status === "running" && task.status === "running";
  const canCompleteTask = !terminal && !taskRunningNow;
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
      // plan 是服务端对「这次为什么走这条路」的只读判定。机械清场做不了时
      // （worktree 有没提交的活 / 合并会冲突）把理由说清楚 —— 否则用户看到
      // 「又跑起 agent 了」只会以为收尾卡住了。
      const planReason = res.plan?.reason?.trim();
      const planFiles = Array.isArray(res.plan?.files) ? (res.plan!.files.filter(Boolean) as string[]) : [];
      const planLine = planReason
        ? planFiles.length > 0
          ? `${t("task.finishWorktreePlanFiles", { files: planFiles.join(", ") })} — ${planReason}`
          : planReason
        : "";
      const message = res.action === "session_running"
        ? t("task.finishWorktreeSessionRunning")
        : res.action === "nudged"
          ? (planLine ? `${t("task.finishWorktreeNudged")} — ${planLine}` : t("task.finishWorktreeNudged"))
          : (planLine ? `${t("task.finishWorktreeStarted")} — ${planLine}` : t("task.finishWorktreeStarted"));
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
    editingName,
    setEditingName,
    nameDraft,
    setNameDraft,
    saving,
    canFinishWorktree,
    terminal,
    canCompleteTask,
    numberLabel,
    saveName,
    finishWorktree,
    completeTask,
    deleteTaskCard,
  };
}