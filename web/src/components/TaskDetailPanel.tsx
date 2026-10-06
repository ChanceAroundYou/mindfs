import React, { useEffect, useRef, useState } from "react";
import { AgentIcon } from "./AgentIcon";
import { ModeIcon } from "./ModeIcon";
import { PromptEditor } from "./PromptEditor";
import { StageEditor } from "./StageEditor";
import { PanelShell, panelButtonStyle, panelIconButtonStyle } from "./PanelShell";
import type { SessionReusePolicy } from "./StageOptionsBar";
import { PencilIcon, TrashIcon, composerInputStyle } from "./action/composerStyles";
import { fileTokenPath, formatFileToken, uploadFiles } from "../services/upload";
import { useI18n, type I18nContextValue } from "../i18n";
import {
  addTaskStage,
  beginTaskFinishWorktree,
  moveTask,
  rebuildTaskWorktree,
  removeTaskStage,
  renameTask,
  updateTaskStage,
  type KanbanTask,
  type StageTemplate,
  type TaskDetail,
} from "../services/tasks";
import { confirmDialog } from "../services/dialog";
import type { AgentStatus } from "../services/agents";
import { reportError } from "../services/error";
import { canAdvanceFromCurrentStage, hasLaterStage, isFinishStageActive, isTerminalKanbanTask, nextRunnableStageIndex, taskStatusColor } from "../app/appTask";
import { DEFAULT_TASK_AGENT, DEFAULT_TASK_MODEL, inheritAgentStage } from "../app/appTask";
import { RunNowIcon, TaskCompleteIcon, TaskFinishWorktreeIcon, TaskQueuedSpinnerIcon, TaskRebuildWorktreeIcon } from "../app/taskIcons";

export type TaskDetailPanelProps = {
  detail: TaskDetail | null;
  agents: AgentStatus[];
  onClose: () => void;
  onOpenSession: (sessionKey: string) => void;
  onMoved?: (detail: TaskDetail) => void;
  /** 与卡片上的「立即执行」同一条路径：App 侧 handleMoveKanbanTask(task, "run-now") */
  onRunTask?: (task: KanbanTask) => void | Promise<void>;
  nodeId?: string;
  /** 节点主题色，用于发送/编辑按钮 */
  accentColor?: string;
};

function statusText(status: string, t: (key: Parameters<I18nContextValue["t"]>[0]) => string): string {
  switch (status) {
    case "pending": return t("task.status.pending");
    case "running": return t("task.status.running");
    case "waiting_user": return t("task.status.waitingUser");
    case "paused": return t("task.status.paused");
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

export function TaskDetailPanel({ detail, agents, onClose, onOpenSession, onMoved, onRunTask, nodeId, accentColor }: TaskDetailPanelProps) {
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

  // 「立即执行」只给一个，长在下一个未执行的 agent 段那一行。
  // 两个判据分别在 appTask 的 nextRunnableStageIndex / canAdvanceFromCurrentStage 里，
  // 放那儿是因为它们是纯函数、可以被单测直接跑（组件内的逻辑只能被正则断言）。
  const runnableStageIndex = nextRunnableStageIndex(detail, task.current_stage_index);
  const advanceable = canAdvanceFromCurrentStage(detail, task.current_stage_index);

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
      const tokens = uploaded.map((file) => formatFileToken(fileTokenPath(file))).join("\n");
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

  // 点播放：复用 App 的 handleMoveKanbanTask，与看板卡片上的「立即执行」同一条路径
  // （那边还会顺带 refreshTaskWorktree —— run-now 可能顺手建出 worktree，面板这侧不重复造）
  const runStage = async () => {
    if (!onRunTask) return;
    try {
      setSaving(true);
      await onRunTask(task);
    } finally { setSaving(false); }
  };

  // 重建 worktree：目录已被删时唯一有用的下一步。走和卡片同一个 API，
  // 但不绕 App 的 handleMoveKanbanTask —— 那边只吃 run-now 这类状态迁移，
  // 面板自己 apply 回来的 detail 才是这里的数据源。
  const worktreeMissing = task?.create_worktree === true && task?.worktree_missing === true;
  const rebuildWorktree = async () => {
    if (!task) return;
    try {
      setSaving(true);
      apply(await rebuildTaskWorktree(task.root_id, task.id, nodeId));
    } catch (error) {
      reportError("file.write_failed", String((error as Error)?.message || t("task.actionFailed")));
    } finally { setSaving(false); }
  };

  // worktree 收尾：追加一段收尾阶段，让 agent 自己提交并合并回主干，成功之后
  // 服务端才拆目录、删分支、搬会话。这一步**不可逆**，所以先弹窗确认。
  //
  // 冲突**不自动 abort**：解到一半的取舍连同 MERGE_MSG 一起丢掉，用户得从头再来。
  // 撞上冲突时服务端会把文件清单播回来（task.finish_teardown），由 App 转成
  // 那个「标题 + 详情列表」弹窗 —— 面板这边不重复渲染一份。
  //
  // 只在「worktree 还真的在」时给：目录已经没了的（worktree_missing）该点的是重建，
  // 收尾无从下手。已经在收尾流程里的也不给（再点是往同一条流程上叠一段）。
  //
  // **终态也给**（2026-10-04 用户要求）：任务跑完了但 worktree 还留着没收，
  // 那才是收尾按钮唯一有意义的时候。服务端 reviveTerminalTask 会把它拉回
  // waiting_user 再推进，所以终态不是拦路虎。
  //
  // 「有 agent 段」这条门控必须与服务端对齐（worktree_finish_stage.go 的
  // lastAgentStage 检查）：收尾段要继承上一段的 agent/模型，没有可继承的就
  // 服务端会拒。与其给一个必然 409 的按钮，不如不给。
  const finishActive = isFinishStageActive(task);
  const hasAgentStage = (task?.stages || []).some((stage) => stage.role === "agent");
  const canFinishWorktree = task?.create_worktree === true
    && !!task?.worktree_path
    && task?.worktree_missing !== true
    && hasAgentStage
    && !finishActive;
  const finishWorktree = async () => {
    if (!task) return;
    if (!await confirmDialog({ message: t("task.finishWorktreeConfirm"), confirmLabel: t("task.finishWorktree"), danger: true })) {
      return;
    }
    try {
      setSaving(true);
      apply(await beginTaskFinishWorktree(task.root_id, task.id, nodeId));
      reportError("file.write_failed", t("task.finishWorktreeStarted"), { severity: "info", recoverable: false });
    } catch (error) {
      reportError("file.write_failed", String((error as Error)?.message || t("task.actionFailed")));
    } finally { setSaving(false); }
  };

  // 「完成」只在**没有下一段**时给，与看板卡片同一套门控（TaskCardRows 的 canComplete）。
  //
  // 末段 waiting_user 时服务端 Next 是直接 finishTask 掉整个任务的（service.go 的 Next
  // 末段分支），所以这一格该给的是「完成」而不是「执行」—— 执行键长在下一段那张卡上，
  // 没有下一段就没有那张卡，末段于是两个键都没有、任务看着像是没法收尾。
  // 有下一段时不给完成：收了尾就看不到下一段了，等于替用户提前结束。
  const terminal = isTerminalKanbanTask(task);
  const stageRunningNow = task.current_stage_status === "running" && task.status === "running";
  const canCompleteTask = !terminal && !finishActive && !hasLaterStage(task) && !stageRunningNow;
  const completeTask = async () => {
    if (!task) return;
    try {
      setSaving(true);
      apply(await moveTask(task.root_id, task.id, "complete", "", nodeId));
    } catch (error) {
      reportError("file.write_failed", String((error as Error)?.message || t("task.actionFailed")));
    } finally { setSaving(false); }
  };

  const numberLabel = task.task_number ? `#${task.task_number}` : "";

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
        /* 编辑态这一格换成输入框，旧名跟着 unmount —— 不再和输入框并排留着 */
        <span style={{ flex: "1 1 auto", minWidth: 0, fontSize: "14px", fontWeight: 700, color: "var(--text-color)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {task.name || t("task.namePlaceholder")}
        </span>
      )}
      /* 状态 / 模板名 / #编号编辑时也留着：它们是别的信息，不是正在被改的那一格 */
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
          {/* 重建 worktree 放面板头部、不放阶段行：阶段行是一张卡一个按钮，
              放里面会对**每一段**都渲染一个（task-22 有 6 段 → 6 个一模一样的重建键）。
              执行键已经不给它让位了（用户定的），这里就是那个唯一的重建入口。 */}
          {worktreeMissing ? (
            <button
              type="button"
              title={t("task.rebuildWorktree")}
              aria-label={t("task.rebuildWorktree")}
              disabled={saving}
              onClick={() => void rebuildWorktree()}
              style={{ ...pencilStyle(false), color: "#d97706", opacity: saving ? 0.4 : 1 }}
            >
              <TaskRebuildWorktreeIcon />
            </button>
          ) : null}
          {/* 收尾 worktree：追加一段收尾阶段让 agent 提交并合并，成功后服务端
              自己清场。目录已经没了的不给 —— 那种情况该点的是上面的重建键。
              已经在收尾里就把按钮换成转圈：它必须是个「正在动」的读数，
              而不是第二个能把收尾再叠一段的按钮。 */}
          {finishActive ? (
            <span
              title={t("task.worktreeFinishingTitle")}
              aria-label={t("task.worktreeFinishingTitle")}
              style={{ ...pencilStyle(false), color: "var(--status-ok)", cursor: "default", display: "inline-flex" }}
            >
              <TaskQueuedSpinnerIcon />
            </span>
          ) : canFinishWorktree ? (
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
          {/* 完成：只在没有下一段时给（canCompleteTask），与看板卡片同一套门控。
              末段 waiting_user 的任务点它 = 服务端 Complete 直接收成 success
              （末段那条 Next 分支的等价物），所以它就是那个局面下唯一能收尾的动作。
              放在面板头部而不是阶段行：完成是任务级动作，阶段行是一段一个键。 */}
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
          <button type="button" aria-label={t("task.renameTask")} title={t("task.renameTask")} onClick={() => setEditingName(true)} style={pencilStyle(false)}>
            <PencilIcon />
          </button>
        </>
      )}
    >
      <div style={{ padding: "12px 14px", display: "flex", flexDirection: "column", gap: "10px" }}>
          {/* 收尾进行中：这一段跑着的整段时间里，面板上唯一该说的话就是「在动」。
              收尾段成功/受阻之后清场由服务端自己接着跑，结论走 WS 的
              task.finish_teardown 回到 App 那个弹窗 / toast —— 面板不存第二份。 */}
          {finishActive ? (
            <div
              data-onboarding="task-finish-active"
              style={{ border: "1px solid var(--status-ok)", borderRadius: "8px", padding: "10px", display: "flex", alignItems: "center", gap: "8px" }}
            >
              <span
                aria-hidden="true"
                style={{
                  width: "12px", height: "12px", flex: "0 0 auto", borderRadius: "999px",
                  border: "2px solid var(--status-ok)", borderTopColor: "transparent",
                  animation: "mindfs-update-spin 0.9s linear infinite",
                }}
              />
              <span style={{ fontSize: "11px", color: "var(--text-secondary)" }}>{t("task.worktreeFinishingTitle")}</span>
            </div>
          ) : null}
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
            /* 「立即执行」长在**下一个未执行的 agent 段**这张卡上（runnableStageIndex），
               跟看板卡片上那个按钮同一个图标、同一个绿（RunNowIcon + runIconButtonStyle）。
               指针所在的段不给：run-now 的语义是「推进到下一段」，按钮在没跑过的卡上、
               动作却是「跳过这张卡」，两处对不上就成了假动作。
               worktree 目录被删**不**顶替这个按钮（用户定的）：点下去服务端会把
               「worktree 目录已不存在」记到任务上，面板右上角就是那个重建入口。
               正在跑 / 已暂停 / 终态 / 收尾中都不给 —— 前三个服务端那边也都是 no-op；
               收尾中更不能给：清场马上就要拆掉 worktree，那时候推进阶段就是对着
               一个即将消失的目录干活。 */
            const canRunStage = index === runnableStageIndex
              && advanceable
              && !editing
              && !!onRunTask
              && !isTerminalKanbanTask(task)
              && !finishActive
              && !(task.current_stage_status === "running" && task.status === "running");
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
                  {/* 左组吃掉剩余空间（flex:1），右边那组自然被顶到行尾 ——
                      不靠 marginLeft:auto：一行里两个 auto 会把剩余空间对半分，
                      中间裂出一道缝。运行键因此紧挨着删除键，和看板卡片一致。 */}
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
                  {/* 右组：运行（绿）/ 跳会话 / 删除，跟阶段名同一行、同一高度、贴右。
                      删除键 30px、其余 22px 是仓库既有搭配（面板工具键比阶段键大），
                      不为对齐去改尺寸 —— 改了两处都得跟着动。
                      auto 归删除键：跳会话键出现时（那段跑过）要跟运行键之间留位，
                      而两键不会同时出现 —— 运行键只给没跑过的段。 */}
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

/* 阶段行右侧那组键里的「运行」键。贴右靠左边那组 flex:1，不靠 marginLeft:auto
   —— 一行里两个 auto 会把剩余空间对半分，中间裂一道缝（见阶段行那段注释）。 */
const runIconButtonStyle: React.CSSProperties = {
  ...sessionIconButtonStyle,
  marginLeft: 0,
  color: "var(--accent-color)",
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

