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
  TaskFinishWorktreeIcon,
  TaskPauseIcon,
  TaskPlanAuxIcon,
  TaskQueuedSpinnerIcon,
  TaskRebuildWorktreeIcon,
  TaskResumeIcon,
  TaskRunNowIcon,
  TaskSessionErrorIcon,
  taskAuxBadgeStyle,
  taskCardIconButtonStyle,
  taskReplyPulseStyle,
  taskWorktreeTagStyle,
} from "../app/taskIcons";

/** 卡片能做的状态迁移。cancel 也在这儿：已结束的任务只剩这一个出口。 */
// rebuild-worktree 不是状态迁移，是把已删的树建回来；服务端不自动做（分支还在不在
// 得用户决定），所以必须是一个能点的动作。
// finish-worktree 也不是状态迁移：它把分支合回主干并拆掉 worktree（wt-finish 的
// 服务端那一半），跑完之后任务本身的状态不变。
export type TaskCardAction = "run-now" | "pause" | "resume" | "complete" | "cancel" | "rebuild-worktree" | "finish-worktree";

/**
 * 「· + 内容」合成一个 flex item。
 *
 * 裸的「·」自己是独立 flex item，换行时可能被留在上一行末尾、把它后面的阶段/状态
 * 推到下一行 —— 排出来就是行首一个孤零零的「· worktree」（实测 161px 卡上必现）。
 * 旧的 nowrap 单行挤不出这种形态，是开了 flexWrap 才有的。合成一对就永不拆散。
 * 不能改用 ::before 伪元素（那样更干净）—— 那需要 className，而本卡的字号必须留在
 * 行内 style 里：index.css 的分区缩放靠 [style*="font-size: Npx"] 属性选择器。
 */
const taskMetaPairStyle: React.CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  gap: "5px",
  flex: "0 1 auto",
  minWidth: 0,
};

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
  // 徽标说的是「**现在**有没有 worktree」，不是「当初要不要建树」。create_worktree
  // 是创建时的配置，永久为 true；收尾之后它一点没变，于是徽标照样显示绿色 worktree，
  // 用户刚点完收尾看到的还是「我有 worktree」。判据必须看 worktree_path 在不在 ——
  // 收尾会把它清空，这就是「已收尾」的信号。
  const worktreeEnabled = task.create_worktree === true;
  // 没开过 worktree 的任务不该因为「path 恰好有值」就显示有树 —— create_worktree
  // 才是「这个任务用不用 worktree」的开关，path 只是结果。
  const hasWorktreePath = worktreeEnabled && !!task.worktree_path;
  // 服务端派生的失效标记：worktree_path 还指着那个目录，但目录已经被删了
  // （DELETE /api/git/worktrees、wt-finish.sh cleanup、手工 rm）。
  const worktreeMissing = worktreeEnabled && task.worktree_missing === true;
  // 收尾过了：当初建过树，现在 path 被清空、目录也没了。
  const worktreeFinished = worktreeEnabled && !hasWorktreePath && !worktreeMissing;
  // 三种「不在」要分开：从来没建（amber）、建过但目录被删（red）、收尾拆掉了（灰）。
  const worktreeTagState = worktreeMissing ? "missing" : worktreeFinished ? "finished" : hasWorktreePath ? "enabled" : "none";
  // 收尾要有可拆的 worktree：目录不在了给的是「重建」，没有 worktree 可拆。
  // 终态任务不给 —— 活已经结束了，没有正在 worktree 里的东西要收。
  const canFinishWorktree = worktreeEnabled && !worktreeMissing && hasWorktreePath && !terminal;
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
          // 窄容器（220px 工作台卡 / 161px 移动端两列）里原来 5 个元素挤一条 nowrap 线，
          // 任务名是唯一可伸缩项，被挤到只剩 23px ≈ 2 个汉字。允许换行后信息分两行各自完整。
          flexWrap: "wrap",
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
        {/* flex-basis 是全部：换行按 base size 判定、收缩只发生在换行之后，
            不给一个明确的小 basis，名字的 auto(=max-content) 仍会被当成占满整行。
            basis 用百分比而非固定 px：固定 px 会在 300~400px 段反常地比 220px 更窄
            （那时阶段/状态刚好都排得下、又把剩余全吃掉的名字挤回去）；50% 各段都 ≥ 它。
            注意 grow 只吃**剩余**空间，拿不走别人的，所以这里不是"名字通吃"。 */}
        <span
          title={taskName}
          style={{
            flex: "1 1 50%",
            minWidth: 0,
            // 2 行截断：与 TaskBoardView 正文折叠同一套写法（仓库先例），不新增 CSS 类。
            display: "-webkit-box",
            WebkitLineClamp: 2,
            WebkitBoxOrient: "vertical",
            overflow: "hidden",
            wordBreak: "break-word",
            overflowWrap: "anywhere",
            fontWeight: 800,
            color: "var(--text-color)",
          }}
        >
          {taskName}
        </span>
        {stageName ? (
          <span style={taskMetaPairStyle}>
            <span style={{ flex: "0 0 auto", opacity: 0.55 }}>·</span>
            <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
              {stageName}
            </span>
          </span>
        ) : null}
        {showStatus ? (
          <>
            <span style={taskMetaPairStyle}>
              <span style={{ flex: "0 0 auto", opacity: 0.55 }}>·</span>
              <span style={{ flex: "0 0 auto", color: taskStatusColor(task.status || ""), fontWeight: 800 }}>{statusText}</span>
            </span>
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
          title={worktreeTagState === "missing"
            ? t("task.worktreeMissingTitle")
            : worktreeTagState === "finished"
              ? t("task.worktreeFinishedTitle")
              : worktreeTagState === "enabled"
                ? t("task.worktreeTitle")
                : t("task.noWorktreeTitle")}
          aria-label={worktreeTagState === "missing"
            ? t("task.worktreeMissingTitle")
            : worktreeTagState === "finished"
              ? t("task.worktreeFinishedTitle")
              : worktreeTagState === "enabled"
                ? t("task.worktreeTitle")
                : t("task.noWorktreeTitle")}
          style={taskWorktreeTagStyle(worktreeTagState)}
        >
          {worktreeTagState === "enabled" || worktreeTagState === "missing" ? null : <NoWorktreeIcon />}
          {worktreeTagState === "finished" ? t("task.worktreeFinishedLabel") : "worktree"}
        </span>
      </div>
      {children}
      <div style={{ marginTop: "8px", display: "flex", alignItems: "center", justifyContent: "space-between", gap: "4px", flexWrap: "wrap" }}>
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
        {/* 换行后靠 marginLeft:auto 仍贴右（space-between 在单行时本来也等效） */}
        <div style={{ display: "flex", justifyContent: "flex-end", gap: 0, marginLeft: "auto" }}>
          {!terminal ? (
            <>
              {/* 失效 worktree：给的是「重建」而不是「执行」——
                  执行必然失败（cwd 就是那个不存在的目录），重建才是有用的下一步。 */}
              {worktreeMissing ? (
                <button
                  type="button"
                  title={t("task.rebuildWorktree")}
                  aria-label={t("task.rebuildWorktree")}
                  onClick={(event) => {
                    event.stopPropagation();
                    onMove(task, "rebuild-worktree");
                  }}
                  style={taskCardIconButtonStyle("warning")}
                >
                  <TaskRebuildWorktreeIcon />
                </button>
              ) : null}
              {showAdvance && !worktreeMissing ? (
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
              {/* 收尾 worktree：活干完了把分支合回主干、拆掉 worktree 和分支。
                  放在执行键右边 —— 两者是任务生命周期的两端（继续跑 / 跑完收掉），
                  挨着才看得出这是一对。目录已经没了的不给，那种情况给的是上面的重建键。 */}
              {canFinishWorktree ? (
                <button
                  type="button"
                  title={t("task.finishWorktree")}
                  aria-label={t("task.finishWorktree")}
                  onClick={(event) => {
                    event.stopPropagation();
                    onMove(task, "finish-worktree");
                  }}
                  style={taskCardIconButtonStyle("success")}
                >
                  <TaskFinishWorktreeIcon />
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
