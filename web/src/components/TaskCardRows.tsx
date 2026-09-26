import React from "react";
import { useI18n } from "../i18n";
import { isTerminalKanbanTask, parseTaskSessionErrorDetails, parseTaskSessionErrorMessage, taskStatusColor, taskStatusLabel } from "../app/appTask";
import { AgentIcon } from "./AgentIcon";
import { ModeIcon } from "./ModeIcon";
import { NoWorktreeIcon } from "./NoWorktreeIcon";
import { renderToolIcon } from "./stream/ToolCallCard";
import type { SessionItem } from "../app/appSession";
import type { KanbanTask } from "../services/tasks";
import {
  DeleteIcon,
  RunNowIcon,
  TaskCompleteIcon,
  TaskPauseIcon,
  TaskPlanAuxIcon,
  TaskQueuedSpinnerIcon,
  TaskResumeIcon,
  TaskRunNowIcon,
  TaskSessionErrorIcon,
  taskAuxBadgeStyle,
  taskCardIconButtonStyle,
  taskReplyPulseStyle,
  taskWorktreeTagStyle,
} from "../app/taskIcons";

/** 卡片能做的状态迁移。cancel 也在这儿：已结束的任务只剩这一个出口。 */
export type TaskCardAction = "run-now" | "pause" | "resume" | "complete" | "cancel";

export type TaskSessionErrorDialog = { title: string; message: string; details: string[] };

export type TaskCardRowsProps = {
  task: KanbanTask;
  /** 编号/名字/阶段名回退用；工作台跨项目时没有「当前模板」概念，传空串即可 */
  templateNameFallback?: string;
  /** 正文是否渲染在两行之间。默认不渲染 —— 工作台只扫读，正文去项目看板看。 */
  children?: React.ReactNode;
  sessionKeys: string[];
  sessionByKey: Record<string, SessionItem>;
  getNodeColor: (rootId: string) => string | null;
  /** 没有就传 0/空串 —— 看板的「跳到这一列」是它自己的滚动行为 */
  onOpenSession: (sessionKey: string, rootId: string, taskId: string) => void;
  onMove: (task: KanbanTask, action: TaskCardAction) => void;
  onShowSessionError: (payload: TaskSessionErrorDialog) => void;
  /** 阶段名只在「执行中」出现（看板按列给，工作台按状态给），传空串不显示 */
  stageName?: string;
  /** 状态文字是否带色显示在工作台上 */
  showStatus?: boolean;
  /** fail 状态已由状态文字表达时，别再在操作行重复一个错误入口 */
  hideSessionError?: boolean;
};

/**
 * 任务卡的两行：标题行（编号 / 名字 / 阶段 / 状态 / worktree）+ 操作行。
 *
 * 项目看板和跨项目工作台**共用这一份**。以前两边各写各的，结果工作台那张卡
 * 只有一个名字和三个按钮，既看不见会话也看不见状态，配色也和看板不一致。
 * 「他们本来就该用同一套组件」—— 所以现在真的是同一个组件。
 *
 * 中间的任务正文**不在这里**：看板要全文 + 展开/折叠，从 children 传进来；
 * 工作台不传，于是那一段自然不存在。正文相关的 props 也就完全不进这个组件 ——
 * 这条由 tests/workspace-board.test.mjs 断言钉住。
 */
export function TaskCardRows({
  task,
  templateNameFallback = "",
  children,
  sessionKeys,
  sessionByKey,
  getNodeColor,
  onOpenSession,
  onMove,
  onShowSessionError,
  stageName = "",
  showStatus = false,
  hideSessionError = false,
}: TaskCardRowsProps) {
  const { t } = useI18n();
  const nodeColor = getNodeColor(String(task.root_id || "")) || "";
  const sessionPending = sessionKeys.some((key) => !!sessionByKey[key]?.pending);
  const queued = task.status === "queued";
  const blockedByConcurrency = queued && !task.scheduler_admitted && !sessionKeys.length;
  const auxFlags = task.aux_flags || {};
  const sessionError = parseTaskSessionErrorMessage(auxFlags.session_error);
  const sessionErrorDetails = parseTaskSessionErrorDetails(auxFlags.session_error);
  const auxBadges = [
    auxFlags.ask_user_waiting ? { key: "ask_user", label: t("task.waitingUser"), icon: renderToolIcon("ask_user"), attention: true } : null,
    auxFlags.has_plan ? { key: "plan", label: t("task.hasPlan"), icon: <TaskPlanAuxIcon color={nodeColor || undefined} />, attention: false } : null,
    auxFlags.has_todos ? { key: "todos", label: t("task.hasTodos"), icon: renderToolIcon("todo"), attention: false } : null,
    auxFlags.has_task ? { key: "task", label: t("task.hasTask"), icon: renderToolIcon("task"), attention: false } : null,
  ].filter((item): item is { key: string; label: string; icon: React.ReactNode; attention: boolean } => Boolean(item));
  const terminal = isTerminalKanbanTask(task);
  // 暂停只在阶段真在跑时给；等待/未开始没有「暂停」可言。恢复同理。
  const stageRunning = task.current_stage_status === "running" && task.status === "running";
  // 完成是纯任务状态操作：不碰会话/worktree，所以任何非终态卡都该给这个出口。
  // 会话被删或 worktree 丢失的任务永远停在 running，只有这条能救。
  const canComplete = !terminal;
  const canPause = stageRunning;
  const canResume = task.status === "paused";
  const showAdvance = !terminal && !stageRunning;
  const statusText = taskStatusLabel(task.status || "", t);
  const worktreeEnabled = task.create_worktree === true;
  const numberLabel = task.task_number ? `#${task.task_number}` : "";
  const taskName = task.name || task.task_template_name || templateNameFallback || t("task.unnamedTemplate");
  const title = task.task_template_name || templateNameFallback || t("task.defaultTitle");
  return (
    <>
      {sessionPending ? (
        <span
          aria-label={t("task.replying")}
          title={t("task.replying")}
          style={taskReplyPulseStyle(nodeColor || undefined)}
        />
      ) : null}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: "5px",
          minWidth: 0,
          color: "var(--text-secondary)",
          fontSize: "10px",
          fontWeight: 700,
          lineHeight: "14px",
        }}
      >
        {numberLabel ? (
          <span style={{ flex: "0 0 auto", color: "#0ea5e9", fontWeight: 800 }}>{numberLabel}</span>
        ) : null}
        <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontWeight: 800, color: "var(--text-color)" }}>
          {taskName}
        </span>
        {stageName ? (
          <>
            <span style={{ flex: "0 0 auto", opacity: 0.55 }}>·</span>
            <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
              {stageName}
            </span>
          </>
        ) : null}
        {showStatus ? (
          <>
            <span style={{ flex: "0 0 auto", opacity: 0.55 }}>·</span>
            <span style={{ flex: "0 0 auto", color: taskStatusColor(task.status || ""), fontWeight: 800 }}>{statusText}</span>
            {task.status === "fail" && sessionError ? (
              <button
                type="button"
                title={t("task.viewError")}
                aria-label={t("task.viewTaskError")}
                onClick={(event) => {
                  event.stopPropagation();
                  onShowSessionError({ title, message: sessionError, details: sessionErrorDetails });
                }}
                style={{ ...taskCardIconButtonStyle("warning"), width: "16px", height: "16px" }}
              >
                <TaskSessionErrorIcon />
              </button>
            ) : null}
          </>
        ) : null}
        <span
          title={worktreeEnabled ? t("task.worktreeTitle") : t("task.noWorktreeTitle")}
          aria-label={worktreeEnabled ? t("task.worktreeTitle") : t("task.noWorktreeTitle")}
          style={taskWorktreeTagStyle(worktreeEnabled)}
        >
          {worktreeEnabled ? null : <NoWorktreeIcon />}
          worktree
        </span>
      </div>
      {children}
      <div style={{ marginTop: "8px", display: "flex", alignItems: "center", justifyContent: "space-between", gap: "4px" }}>
        <div style={{ display: "flex", alignItems: "center", gap: "4px" }}>
          {sessionKeys.length > 0 ? (
            sessionKeys.map((sessionKey, sessionIndex) => {
              const session = sessionByKey[sessionKey] || null;
              return (
                <button
                  key={`${task.id}-${sessionKey}`}
                  type="button"
                  title={session?.name || t("task.openSession", { index: sessionIndex + 1 })}
                  aria-label={t("task.openSession", { index: sessionIndex + 1 })}
                  onClick={(event) => {
                    event.stopPropagation();
                    onOpenSession(sessionKey, task.root_id || "", task.id);
                  }}
                  style={taskCardIconButtonStyle()}
                >
                  <span
                    style={{
                      position: "relative",
                      width: "18px",
                      height: "18px",
                      display: "inline-flex",
                      alignItems: "center",
                      justifyContent: "center",
                    }}
                  >
                    <ModeIcon type="task" size={16} />
                    <span
                      style={{
                        position: "absolute",
                        right: "-2px",
                        bottom: "-2px",
                        width: "10px",
                        height: "10px",
                        borderRadius: "999px",
                        background: "var(--content-bg, #fff)",
                        border: "1px solid rgba(255,255,255,0.9)",
                        display: "flex",
                        alignItems: "center",
                        justifyContent: "center",
                        overflow: "hidden",
                      }}
                    >
                      <AgentIcon
                        agentName={session?.agent || ""}
                        style={{ width: "10px", height: "10px", display: "block" }}
                      />
                    </span>
                  </span>
                </button>
              );
            })
          ) : queued ? (
            <>
              <span
                title={t("task.waitingSchedule")}
                aria-label={t("task.waitingSchedule")}
                style={{
                  ...taskCardIconButtonStyle(),
                  cursor: "default",
                  color: "var(--accent-color)",
                }}
              >
                <TaskQueuedSpinnerIcon />
              </span>
              {blockedByConcurrency ? (
                <button
                  type="button"
                  title={t("task.runNow")}
                  aria-label={t("task.runNow")}
                  onClick={(event) => {
                    event.stopPropagation();
                    onMove(task, "run-now");
                  }}
                  style={{
                    ...taskCardIconButtonStyle(),
                    width: "17px",
                    marginLeft: "-3px",
                    color: "var(--accent-color)",
                  }}
                >
                  <TaskRunNowIcon />
                </button>
              ) : null}
            </>
          ) : null}
          {sessionError && !hideSessionError ? (
            <button
              type="button"
              title={t("task.viewError")}
              aria-label={t("task.viewTaskSessionError")}
              onClick={(event) => {
                event.stopPropagation();
                onShowSessionError({ title, message: sessionError, details: sessionErrorDetails });
              }}
              style={taskCardIconButtonStyle("warning")}
            >
              <TaskSessionErrorIcon />
            </button>
          ) : null}
          {auxBadges.length > 0 ? (
            <div style={{ display: "inline-flex", alignItems: "center", gap: "2px" }}>
              {auxBadges.map((badge) => (
                <span
                  key={badge.key}
                  title={badge.label}
                  aria-label={badge.label}
                  style={taskAuxBadgeStyle(badge.attention)}
                >
                  {badge.icon}
                </span>
              ))}
            </div>
          ) : null}
        </div>
        {/* 已结束（success/fail/cancelled）的任务也要留得住「删除」：
            会话没了、worktree 被删导致卡住的任务，卡片上仍得能清理。
            执行 / 完成只对未结束的任务有意义。 */}
        <div style={{ display: "flex", justifyContent: "flex-end", gap: 0 }}>
          {!terminal ? (
            <>
              {showAdvance ? (
                <button
                  type="button"
                  title={t("task.runNow")}
                  aria-label={t("task.runNow")}
                  onClick={(event) => {
                    event.stopPropagation();
                    onMove(task, "run-now");
                  }}
                  style={taskCardIconButtonStyle("accent")}
                >
                  <RunNowIcon />
                </button>
              ) : null}
              {canPause ? (
                <button
                  type="button"
                  title={t("task.status.paused")}
                  aria-label={t("task.actionPause")}
                  onClick={(event) => {
                    event.stopPropagation();
                    onMove(task, "pause");
                  }}
                  style={taskCardIconButtonStyle()}
                >
                  <TaskPauseIcon />
                </button>
              ) : null}
              {canResume ? (
                <button
                  type="button"
                  title={t("task.actionResume")}
                  aria-label={t("task.actionResume")}
                  onClick={(event) => {
                    event.stopPropagation();
                    onMove(task, "resume");
                  }}
                  style={taskCardIconButtonStyle("accent")}
                >
                  <TaskResumeIcon />
                </button>
              ) : null}
              {canComplete ? (
                <button
                  type="button"
                  title={t("task.completeShort")}
                  aria-label={t("task.complete")}
                  onClick={(event) => {
                    event.stopPropagation();
                    onMove(task, "complete");
                  }}
                  style={taskCardIconButtonStyle("success")}
                >
                  <TaskCompleteIcon />
                </button>
              ) : null}
            </>
          ) : null}
          <button
            type="button"
            title={t("common.delete")}
            aria-label={t("task.delete")}
            onClick={(event) => {
              event.stopPropagation();
              onMove(task, "cancel");
            }}
            style={taskCardIconButtonStyle("danger")}
          >
            <DeleteIcon />
          </button>
        </div>
      </div>
    </>
  );
}
