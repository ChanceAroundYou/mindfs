import { useEffect, useRef, useState } from "react";
import { confirmDialog } from "../../services/dialog";
import {
  archiveTask,
  beginTaskFinishWorktree,
  deleteTask,
  fetchTaskDetails,
  isTaskGoneError,
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
  onTaskGone,
  t,
}: {
  detail: TaskDetail | null;
  nodeId?: string;
  onClose: () => void;
  onMoved?: (detail: TaskDetail) => void;
  /** 任务在服务端已不存在：让看板把那卡片摘掉（缓存 + 内存）。 */
  onTaskGone?: (rootId: string, taskId: string) => void;
  t: (key: any, fallback?: any) => string;
}) {
  const task = detail?.task || null;

  const [nameDraft, setNameDraft] = useState("");
  const [editingName, setEditingName] = useState(false);
  const [saving, setSaving] = useState(false);
  const refreshingRef = useRef(false);
  const onMovedRef = useRef(onMoved);
  onMovedRef.current = onMoved;

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

  // 打开面板时先拉一次最新详情：看板快照可能是完成前缓存的，用户会看到
  // 「任务已完成、头部还写待开始」。只按 task_number 点查，不扇出整个看板。
  useEffect(() => {
    if (!task?.root_id || !task.task_number) return;
    let cancelled = false;
    void fetchTaskDetails(task.root_id, { taskNumber: task.task_number }, nodeId)
      .then((items) => {
        if (cancelled || !items[0]) return;
        onMovedRef.current?.(items[0]);
      })
      .catch(() => {
        // 详情刷新失败不影响面板已有内容；下一次活动更新或手动刷新会再试。
      });
    return () => {
      cancelled = true;
    };
  }, [task?.id, task?.root_id, task?.task_number, nodeId]);

  if (!task || !detail) return null;

  const terminal = isTerminalKanbanTask(task);
  const paused = task.status === "paused";
  const numberLabel = task.task_number ? `#${task.task_number}` : "";

  const apply = (next: TaskDetail) => onMoved?.(next);
  const fail = (err: unknown) => {
    // 幽灵卡：服务端已经没有这条任务（别的设备删掉了 / 本页断线时漏了广播）。
    // 面板留着它，只会让改名、完成、删除每个动作再失败一次 —— 摘卡 + 关面板，
    // 并把「为什么」说清楚，而不是甩一句 sql: no rows in result set。
    if (isTaskGoneError(err)) {
      onTaskGone?.(task.root_id || "", task.id);
      reportError("file.write_failed", t("task.goneRemoved"), { severity: "info", recoverable: false });
      onClose();
      return;
    }
    reportError("file.write_failed", String((err as Error)?.message || t("task.actionFailed")));
  };

  // 活动流每次看到 Agent 回复状态变化时调用；只做点查并交给父层合并。
  const refreshTask = async () => {
    if (!task.root_id || !task.task_number || refreshingRef.current) return;
    refreshingRef.current = true;
    try {
      const items = await fetchTaskDetails(task.root_id, { taskNumber: task.task_number }, nodeId);
      if (items[0]) apply(items[0]);
    } catch {
      // 只读刷新失败不打断对话；下一次状态变化会再试。
    } finally {
      refreshingRef.current = false;
    }
  };

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
        : (planLine ? `${t("task.finishWorktreeStarted")} — ${planLine}` : t("task.finishWorktreeStarted"));
      reportError("file.write_failed", message, { severity: "info", recoverable: false });
    } catch (error) { fail(error); } finally { setSaving(false); }
  };

  const completeTask = async () => {
    if (!task) return;
    try {
      setSaving(true);
      apply(await moveTask(task.root_id, task.id, "complete", "", nodeId));
    } catch (error) { fail(error); } finally { setSaving(false); }
  };

  // 取消与归档都只改状态/标记，卡片不会被删掉 —— 删除是独立的破坏性动作。
  const cancelTask = async () => {
    if (!task) return;
    try {
      setSaving(true);
      apply(await moveTask(task.root_id, task.id, "cancel", "", nodeId));
    } catch (error) { fail(error); } finally { setSaving(false); }
  };

  const setArchived = async (archived: boolean) => {
    if (!task) return;
    try {
      setSaving(true);
      apply(await archiveTask(task.root_id, task.id, archived, nodeId));
    } catch (error) { fail(error); } finally { setSaving(false); }
  };

  // 「开始」：未开始的任务由 header 的开始键发起（发出排队留言，任务进入运行中）。
  // 对话流里那条排队留言的「立即执行」仍走 runQueued → runTaskNow，两条入口落到
  // 同一个后端 RunNow，不会各跑一遍。
  const startTask = async () => {
    if (!task) return;
    try {
      setSaving(true);
      apply(await moveTask(task.root_id, task.id, "start", "", nodeId));
    } catch (error) { fail(error); } finally { setSaving(false); }
  };

  // 「终止」：停掉在跑的会话 / 工作流，任务回到待处理。落点不是取消 ——
  // 用户还可以接着说话，旧现场（Run 投影、回复行、转录）原样保留。
  const terminateTask = async () => {
    if (!task) return;
    try {
      setSaving(true);
      apply(await moveTask(task.root_id, task.id, "terminate", "", nodeId));
    } catch (error) { fail(error); } finally { setSaving(false); }
  };

  const setPaused = async (next: "pause" | "resume") => {
    if (!task) return;
    try {
      setSaving(true);
      apply(await moveTask(task.root_id, task.id, next, "", nodeId));
    } catch (error) { fail(error); } finally { setSaving(false); }
  };

  // 回到待处理：会话输入框会随状态重新打开，旧轮次的 Run / 回复原样保留。
  const reopenTask = async () => {
    if (!task) return;
    try {
      setSaving(true);
      apply(await moveTask(task.root_id, task.id, "reopen", "", nodeId));
    } catch (error) { fail(error); } finally { setSaving(false); }
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
    } catch (error) { fail(error); } finally { setSaving(false); }
  };

  return {
    task,
    editingName,
    setEditingName,
    nameDraft,
    setNameDraft,
    saving,
    terminal,
    paused,
    pauseTask: () => setPaused("pause"),
    resumeTask: () => setPaused("resume"),
    startTask,
    terminateTask,
    cancelTask,
    archiveTaskCard: () => setArchived(true),
    unarchiveTaskCard: () => setArchived(false),
    numberLabel,
    refreshTask,
    saveName,
    finishWorktree,
    completeTask,
    reopenTask,
    deleteTaskCard,
  };
}