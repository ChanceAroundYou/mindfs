import React from "react";
import { useI18n } from "../../i18n/index";
import { parseTaskSessionErrorDetails, parseTaskSessionErrorMessage, taskStatusColor, taskStatusLabel } from "../../app/appTask";
import { AgentIcon } from "../agent/AgentIcon";
import { ModeIcon } from "../agent/ModeIcon";
import { NoWorktreeIcon } from "../common/NoWorktreeIcon";
import { renderToolIcon } from "../stream/toolCallIcons";
import { summaryPlainText } from "../../shared/summaryText";
import type { KanbanTask } from "../../shared/taskTypes";
import type { SessionPresentationSummary } from "../../shared/sessionPresentationTypes";
import type { TaskCardAction, TaskSessionErrorDialog } from "./taskBoardTypes";
export type { TaskCardAction, TaskSessionErrorDialog } from "./taskBoardTypes";
import { TaskCardFinishButton } from "./TaskCardFinishButton";
import { TASK_ACTION_BUTTON_META, taskActionButtons } from "./taskCardActions";
import { taskActionIcons } from "./taskActionIcons";
import { taskRouterAlertBadge } from "./routerAlertModel";
import { taskRouterAskBadge } from "./routerAskModel";
import { attentionSeverityColor, normalizeAttentionSeverity } from "./ActivityMessage";
import { activityMetaPillStyle } from "./ActivityTypeTag";
import {
  TASK_CONTROL_GAP,
  TaskPlanAuxIcon,
  TaskRouterAlertIcon,
  TaskRouterAskIcon,
  TaskSessionErrorIcon,
  taskActionIconStyle,
  taskAuxBadgeStyle,
  taskAuxIconScaleStyle,
  taskCardIconButtonStyle,
  taskReplyPulseStyle,
  taskWorktreeTagStyle,
  type WorktreeTagState,
} from "./taskIcons";

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


export type TaskCardRowsProps = {
  task: KanbanTask;
  /** 编号/名字/阶段名回退用；工作台跨项目时没有「当前任务」概念，传空串即可 */
  taskNameFallback?: string;
  /** 正文是否渲染在两行之间。默认不渲染 —— 工作台只扫读，正文去项目看板看。 */
  children?: React.ReactNode;
  sessionKeys: string[];
  sessionByKey: Record<string, SessionPresentationSummary>;
  getNodeColor: (rootId: string) => string | null;
  /** 没有就传 0/空串 —— 看板的「跳到这一列」是它自己的滚动行为 */
  onOpenSession: (sessionKey: string, rootId: string, taskId: string) => void;
  onMove: (task: KanbanTask, action: TaskCardAction) => void;
  onShowSessionError: (payload: TaskSessionErrorDialog) => void;
  /** 状态文字是否带色显示在工作台上 */
  showStatus?: boolean;
  /** fail 状态已由状态文字表达时，别再在操作行重复一个错误入口 */
  hideSessionError?: boolean;
};

/**
 * 任务卡的两行：标题行（编号 / 名字 / 状态 / worktree）+ 操作行。
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
  taskNameFallback = "",
  children,
  sessionKeys,
  sessionByKey,
  getNodeColor,
  onOpenSession,
  onMove,
  onShowSessionError,
  showStatus = false,
  hideSessionError = false,
}: TaskCardRowsProps) {
  const { t } = useI18n();
  const nodeColor = getNodeColor(String(task.root_id || "")) || "";
  const sessionPending = sessionKeys.some((key) => !!sessionByKey[key]?.pending);
  const auxFlags = task.aux_flags || {};
  const sessionError = parseTaskSessionErrorMessage(auxFlags.session_error);
  const sessionErrorDetails = parseTaskSessionErrorDetails(auxFlags.session_error);
  // Router 角标（W3-4）：健康事件还没被 Router 处理时点亮，处理完服务端就
  // 不再下发 router_alert，卡片随之恢复干净 —— 语义在服务端，这里只负责画。
  const routerAlert = taskRouterAlertBadge(task);
  // Router 自己派发的提问（与 worker 透传 ask 分源）。两个徽标各有 key 与文案，
  // 同时存在时并列显示，不合并也不互相置位。
  const routerAsk = taskRouterAskBadge(task);
  // 最新一条提醒：沿用详情里“类型胶囊 + 正文”的样式，颜色由 severity 决定。
  const latestAttention = task.latest_attention;
  const latestAttentionText = latestAttention?.body ? summaryPlainText(latestAttention.body) : "";
  const latestAttentionTone = attentionSeverityColor(latestAttention?.severity);
  // 摘要按纯文本渲染（S8）：卡片没有 Markdown 解析器，`**结论**`、反引号与表格
  // 原样显示就是一串符号。title 与正文同源，悬停看到的与卡片上的一致。
  const resultSummary = task.result_summary ? summaryPlainText(task.result_summary) : "";
  const auxBadges = [
    auxFlags.ask_user_waiting ? { key: "ask_user", label: t("task.waitingUser"), icon: renderToolIcon("ask_user"), attention: true } : null,
    routerAsk
      ? {
          key: routerAsk.key,
          label: routerAsk.question
            ? t("task.routerAskWaitingTitle", { question: routerAsk.question })
            : t("task.routerAskWaiting", { count: String(routerAsk.count) }),
          icon: <TaskRouterAskIcon />,
          attention: routerAsk.attention,
        }
      : null,
    routerAlert
      ? {
          key: routerAlert.key,
          label: t(routerAlert.labelKey, { count: String(routerAlert.count) }),
          icon: <TaskRouterAlertIcon color={routerAlert.key === "router_interrupted" ? "#ef4444" : undefined} />,
          attention: routerAlert.attention,
        }
      : null,
    auxFlags.has_plan ? { key: "plan", label: t("task.hasPlan"), icon: <TaskPlanAuxIcon color={nodeColor || undefined} />, attention: false } : null,
    auxFlags.has_todos ? { key: "todos", label: t("task.hasTodos"), icon: renderToolIcon("todo"), attention: false } : null,
    auxFlags.has_task ? { key: "task", label: t("task.hasTask"), icon: renderToolIcon("task"), attention: false } : null,
  ].filter((item): item is { key: string; label: string; icon: React.ReactNode; attention: boolean } => Boolean(item));
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
  // 「建过」的判据：worktree_built 或 path 非空 —— path 非空本身就证明建过。
  const worktreeBuilt = worktreeEnabled && (task.worktree_built === true || hasWorktreePath);
  // 已收尾 = 建过、但目录此刻不在（path 被清空，或 path 还在而目录已经被删）。
  //
  // **目录消失是金标准**：不管字段标着什么，没有目录就既没有树可执行、也没有树可拆，
  // 读「已收尾」比读「红色失效」诚实。以前这里把「建过但目录被删」判成红色的 missing，
  // 实测 14 个早已收完尾的历史任务全是这个形状 —— 挂着一个没人能兑现的红色警报。
  //
  // 反过来也不能只看 create_worktree：它永久为 true，收尾之后一点没变，
  // 光看它会让收完尾的任务照样显示「有 worktree」。
  const worktreeGone = worktreeBuilt && (!hasWorktreePath || worktreeMissing);
  // 三档：从来没建（amber 禁止符）、建着（绿）、收尾拆掉了（灰「已收尾」）。
  const worktreeTagState: WorktreeTagState = worktreeGone
    ? "finished"
    : hasWorktreePath
      ? "enabled"
      : "none";
  const worktreeTagTitle = worktreeTagState === "finished"
    ? t("task.worktreeFinishedTitle")
    : worktreeTagState === "enabled"
      ? t("task.worktreeTitle")
      : t("task.noWorktreeTitle");
  // 收尾要有可拆的 worktree：目录不在了给的是「重建」，没有 worktree 可拆。
  // **终态也给**（与 TaskDetailPanel 同步）：任务跑完但 worktree 还留着没收，
  // 那正是收尾唯一有意义的场景；服务端会把它复活成 running 再起收尾回合。
  // 收尾进行中照样可点，且必须幂等 —— 收尾回合卡住时，用户能再点一次走机械清场
  // （后端按「分支是否已合进主干」分流，见 handleKanbanTaskBeginFinish）。
  // 卡片与任务详情 header 读同一份「实际渲染的图标键」（taskActionButtons）：
  // 两边各抄一遍条件，就是「卡片有、详情没有」的来源。暂停只在一轮真的在跑时给；
  // 等待/未开始没有「暂停」可言，恢复同理。
  const buttons = taskActionButtons(task);
  const numberLabel = task.task_number ? `#${task.task_number}` : "";
  const taskName = task.name || task.task_template_name || taskNameFallback || t("task.unnamed");
  const title = task.task_template_name || taskNameFallback || t("task.defaultTitle");
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
          title={worktreeTagTitle}
          aria-label={worktreeTagTitle}
          style={taskWorktreeTagStyle(worktreeTagState)}
        >
          {worktreeTagState === "enabled" ? null : <NoWorktreeIcon />}
          {worktreeTagState === "finished" ? t("task.worktreeFinishedLabel") : "worktree"}
        </span>
      </div>
      {children}
      {latestAttentionText ? (
        <div
          data-testid="task-card-attention"
          data-severity={normalizeAttentionSeverity(latestAttention?.severity)}
          title={latestAttentionText}
          style={{
            marginTop: "6px",
            display: "flex",
            alignItems: "flex-start",
            gap: "5px",
            minWidth: 0,
          }}
        >
          <span style={{ ...activityMetaPillStyle, color: latestAttentionTone, flex: "0 0 auto" }}>
            {t("task.activity.type.attention")}
          </span>
          <span
            style={{
              color: latestAttentionTone,
              fontSize: "11px",
              lineHeight: "16px",
              display: "-webkit-box",
              WebkitLineClamp: 2,
              WebkitBoxOrient: "vertical",
              overflow: "hidden",
              wordBreak: "break-word",
              overflowWrap: "anywhere",
            }}
          >
            {latestAttentionText}
          </span>
        </div>
      ) : null}
      {resultSummary ? (
        <div
          data-testid="task-card-result"
          title={resultSummary}
          style={{
            marginTop: "6px",
            fontSize: "11px",
            lineHeight: "16px",
            color: "var(--text-secondary)",
            // 看板卡与工作台卡宽度都窄；3 行足够看出「做到了什么」，全文点进详情。
            display: "-webkit-box",
            WebkitLineClamp: 3,
            WebkitBoxOrient: "vertical",
            overflow: "hidden",
            wordBreak: "break-word",
            overflowWrap: "anywhere",
            whiteSpace: "pre-wrap",
          }}
        >
          {resultSummary}
        </div>
      ) : null}
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
                  <span style={taskAuxIconScaleStyle()}>{badge.icon}</span>
                </span>
              ))}
            </div>
          ) : null}
        </div>
        {/* 动作行与详情 header 同源：键序来自 taskActionButtons，文案与配色来自
            TASK_ACTION_BUTTON_META。未结束 → 收尾/暂停/恢复/完成/取消；
            终态 → 收尾/返回/归档/删除；归档 → 返回/取消归档/删除。 */}
        {/* 换行后靠 marginLeft:auto 仍贴右（space-between 在单行时本来也等效） */}
        {/* 键间距与详情 header 同一条 TASK_CONTROL_GAP；按钮自身 flexShrink:0
            （见 taskActionIconStyle），窄卡上也保持 22×22。 */}
        <div style={{ display: "flex", justifyContent: "flex-end", gap: TASK_CONTROL_GAP, marginLeft: "auto" }}>
          {buttons.map((key) => {
            if (key === "finish-worktree") {
              return <TaskCardFinishButton key={key} task={task} onMove={onMove} />;
            }
            const meta = TASK_ACTION_BUTTON_META[key];
            return (
              <button
                key={key}
                type="button"
                title={t(meta.labelKey)}
                aria-label={t(meta.labelKey)}
                onClick={(event) => {
                  event.stopPropagation();
                  onMove(task, key);
                }}
                style={taskActionIconStyle(meta.tone)}
              >
                {taskActionIcons[key]}
              </button>
            );
          })}
        </div>
      </div>
    </>
  );
}
