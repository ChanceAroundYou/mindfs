import { useEffect, useRef, useState } from "react";
import { useI18n } from "../../i18n/index";
import { useTaskActivities } from "../../hooks/useTaskActivities";
import { usePendingAttachments } from "../action/usePendingAttachments";
import type { TaskActivity, WorkflowChoice } from "../../shared/taskComments";
import { pendingTaskPrompt, visibleConversationActivities } from "../../shared/activityModel";
import { ActivityInput } from "./ActivityInput";
import { ActivityMessage } from "./ActivityMessage";
import { ScrollToBottomButton } from "../common/ScrollToBottomButton";
import { alignPanelToBottom, findScrollableAncestor } from "./conversationScroll";
import { useConversationJumpToLatest } from "./useConversationJumpToLatest";
import { useWorkflowNames } from "./useWorkflowNames";

export type ConversationMessagesProps = {
  activities: TaskActivity[];
  rootId?: string;
  /** workflow id → 友好名；页脚显示名字而不显示版本 id。 */
  workflowNames?: Record<string, string> | null;
  paused?: boolean;
  promptDeciding?: boolean;
  promptError?: string | null;
  loading?: boolean;
  error?: string | null;
  onReload?: () => void;
  onOpenSession?: (sessionKey: string) => void;
  onAnswerQuestion?: (
    activity: TaskActivity,
    answers: Record<string, string>,
  ) => void | Promise<void>;
  onGateDecision?: (
    activity: TaskActivity,
    decision: "approve" | "reject",
    note?: string,
  ) => void | Promise<void>;
  onPauseTask?: () => void | Promise<void>;
  onResumeTask?: () => void | Promise<void>;
  /** 排队留言上的「立即执行」。 */
  onRunQueued?: (activity: TaskActivity) => void | Promise<void>;
  runningQueued?: boolean;
};

/**
 * 纯展示的对话流。单独导出是为了能在 node 环境直接 SSR 测试 ——
 * 连接层的 ConversationView 会先经过 hook，effect 不跑时只能看到 loading。
 */
export function ConversationMessages({
  activities,
  rootId,
  workflowNames,
  paused = false,
  promptDeciding = false,
  promptError = null,
  loading = false,
  error,
  onReload,
  onOpenSession,
  onAnswerQuestion,
  onGateDecision,
  onPauseTask,
  onResumeTask,
  onRunQueued,
  runningQueued = false,
}: ConversationMessagesProps) {
  const { t } = useI18n();

  if (loading) {
    return (
      <div data-testid="conversation-loading" style={{ fontSize: 12, color: "var(--text-secondary)" }}>
        {t("common.loading")}
      </div>
    );
  }

  if (error) {
    return (
      <div data-testid="conversation-error" style={{ fontSize: 12, color: "#dc2626", display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
        <span>
          {t("task.activity.loadFailed")}: {error}
        </span>
        {onReload && (
          <button
            type="button"
            data-testid="conversation-retry"
            onClick={onReload}
            style={{ fontSize: 11, padding: "3px 8px", borderRadius: 4, border: "1px solid var(--border-color)", background: "transparent", color: "inherit", cursor: "pointer" }}
          >
            {t("task.activity.retry")}
          </button>
        )}
      </div>
    );
  }

  if (activities.length === 0) {
    return (
      <div data-testid="conversation-empty" style={{ fontSize: 12, color: "var(--text-secondary)" }}>
        {t("task.activity.empty")}
      </div>
    );
  }

  // 进度汇报（purpose=progress）按时间顺序原样平铺：用户需要看到流程跑到了哪一步，
  // 折叠只会把它藏起来。区分靠每行上的浅色「进度」标记。
  const messageProps = {
    rootId,
    workflowNames,
    paused,
    promptDeciding,
    promptError,
    onOpenSession,
    onAnswerQuestion,
    onGateDecision,
    onPauseTask,
    onResumeTask,
    onRunQueued,
    runningQueued,
  };

  return (
    <div
      data-testid="conversation-messages"
      style={{ display: "flex", flexDirection: "column", gap: 10 }}
    >
      {visibleConversationActivities(activities).map((activity) => (
        <ActivityMessage key={activity.id} activity={activity} {...messageProps} />
      ))}
    </div>
  );
}

export type ConversationViewProps = {
  taskId: string;
  rootId: string;
  nodeId?: string;
  /** 终态任务不能再发留言；输入框会换成一句说明。 */
  disabled?: boolean;
  /** 任务已暂停：HumanGate 卡只给恢复入口。 */
  paused?: boolean;
  /** Agent 回复的状态/摘要变化时通知父层刷新任务头（进行中/已完成）。 */
  onAgentActivityChange?: () => void;
  onOpenSession?: (sessionKey: string) => void;
  onPauseTask?: () => void | Promise<void>;
  onResumeTask?: () => void | Promise<void>;
};

/**
 * 对话式任务详情主体：留言流 + 输入框。
 *
 * 数据全部来自 useTaskActivities（REST 首拉 + WS 增量 + 重连补偿），
 * 输入框只持有本地草稿；发送成功后清空草稿，失败时保留内容并展示 sendError。
 */
export function ConversationView({
  taskId,
  rootId,
  nodeId,
  disabled = false,
  paused = false,
  onAgentActivityChange,
  onOpenSession,
  onPauseTask,
  onResumeTask,
}: ConversationViewProps) {
  const { t } = useI18n();
  const {
    activities,
    loading,
    error,
    reload,
    send,
    sending,
    sendError,
    uploadProgress,
    cancelUpload,
    answerQuestion,
    decideGate,
    runQueued,
    runningQueued,
    promptDeciding,
    promptError,
  } = useTaskActivities(taskId, rootId, nodeId);
  const [body, setBody] = useState("");
  const [choice, setChoice] = useState<WorkflowChoice>("empty");
  const [versionId, setVersionId] = useState("");
  const [composerResetKey, setComposerResetKey] = useState(0);
  const {
    pendingAttachments,
    appendPendingAttachments,
    removePendingAttachment,
    clearPendingAttachments,
  } = usePendingAttachments();
  const workflowNames = useWorkflowNames();
  const pendingPrompt = pendingTaskPrompt(activities);
  const lastAgentReplyRef = useRef("");
  const scrollAnchorRef = useRef<HTMLDivElement>(null);
  const initialScrollKeyRef = useRef("");
  // 用户上翻后才出现的「回到底部」：显隐与收敛复用会话时间线那一份。
  const { visible: jumpVisible, jumpToLatest } = useConversationJumpToLatest(scrollAnchorRef, taskId);

  // Agent 回复从 pending → running → completed 时，任务头也要跟着变。
  // 活动流有独立的 WS 增量，任务头依赖 task.updated；两者丢一个就会出现
  // 「回复已完成、头部还写待开始」。这里只对最近一条 agent_reply 的身份/状态
  // 变化触发一次点查，不随流式正文的每个 chunk 扇出请求。
  useEffect(() => {
    let latest: TaskActivity | null = null;
    for (let i = activities.length - 1; i >= 0; i -= 1) {
      if (activities[i]?.type === "agent_reply") {
        latest = activities[i];
        break;
      }
    }
    if (!latest) return;
    const key = `${latest.id}:${latest.status || ""}:${latest.summary || ""}`;
    if (key === lastAgentReplyRef.current) return;
    lastAgentReplyRef.current = key;
    onAgentActivityChange?.();
  }, [activities, onAgentActivityChange]);

  // 打开详情时直接落到面板真正的底部：最新一条回复 + 输入框，以及桌面端位于
  // 对话下方的两个胶囊与收尾区（只对齐对话末尾会把它们留在视口外）。
  //
  // 只在「这个任务的内容第一次到齐」时滚一次：以首条活动 id 作为这一轮的身份，
  // 任务切换或首拉完成会换 key；之后的新留言/流式回复不抢滚动位置（用户可能正在
  // 上翻历史）。布局稳定前重复对齐几帧，避免 Markdown/表格铺开后又差一截；一旦发现
  // 滚动位置不是上一次自己写下的值，说明用户已经手动滚动，立即让位。
  useEffect(() => {
    if (loading && activities.length === 0) return;
    const key = `${taskId}:${activities[0]?.id || "empty"}`;
    if (initialScrollKeyRef.current === key) return;
    initialScrollKeyRef.current = key;
    const anchor = scrollAnchorRef.current;
    if (!anchor) return;
    let cancelled = false;
    let frame = 0;
    let stable = 0;
    let lastHeight = -1;
    let lastApplied = -1;
    const step = () => {
      if (cancelled) return;
      frame += 1;
      const scroller = findScrollableAncestor(anchor);
      if (scroller) {
        if (lastApplied >= 0 && Math.abs(scroller.scrollTop - lastApplied) > 2) return;
        alignPanelToBottom(anchor);
        lastApplied = scroller.scrollTop;
        stable = scroller.scrollHeight === lastHeight ? stable + 1 : 0;
        lastHeight = scroller.scrollHeight;
      }
      if (frame < 24 && stable < 3) requestAnimationFrame(step);
    };
    requestAnimationFrame(step);
    return () => {
      cancelled = true;
    };
  }, [taskId, loading, activities]);

  const handleSend = async (text: string) => {
    try {
      await send(text, choice, versionId, pendingAttachments);
      setBody("");
      setChoice("empty");
      setVersionId("");
      clearPendingAttachments();
      // PromptEditor 内部持有文本；父层清空后必须换一个 resetKey 才会真正清空编辑器。
      setComposerResetKey((key) => key + 1);
    } catch {
      // 错误已由 hook 写入 sendError；草稿与附件保留，用户改完可以直接重发。
    }
  };

  return (
    // 内部间距一律用显式 margin，不用 flex 的 gap：这里的「回到底部」浮层与滚动锚点
    // 都是零高度元素，flex gap 会照样在它们两侧各加一份，实测把「对话 → 输入框」从
    // 16px 撑成 32px、「输入框 → 面板下方胶囊」多出 16px 死区。
    <div
      data-testid="conversation-view"
      style={{ display: "flex", flexDirection: "column", minWidth: 0, maxWidth: "100%" }}
    >
      <ConversationMessages
        activities={activities}
        rootId={rootId}
        workflowNames={workflowNames}
        paused={paused}
        promptDeciding={promptDeciding}
        promptError={promptError}
        loading={loading && activities.length === 0}
        // 重连补偿失败时不要把已经看到的对话换成整屏错误；只有首拉失败才让错误占位。
        error={activities.length === 0 ? error : null}
        onReload={() => void reload()}
        onOpenSession={onOpenSession}
        onAnswerQuestion={answerQuestion}
        onGateDecision={decideGate}
        onPauseTask={onPauseTask}
        onResumeTask={onResumeTask}
        onRunQueued={disabled ? undefined : async () => {
          await runQueued();
          // 任务头（待开始）与 Agent 回复都变了，让详情面板重新点查一次。
          onAgentActivityChange?.();
        }}
        runningQueued={runningQueued}
      />

      {/*
        「回到底部」浮在输入框上方。外层始终存在（零高度 + sticky），零高度又不带
        margin，所以按钮显隐不改变对话流的间距；内层用 absolute 定位，和会话浮层
        同一套位置与尺寸。
      */}
      <div
        style={{
          position: "sticky",
          bottom: 0,
          height: 0,
          zIndex: 5,
          pointerEvents: "none",
        }}
      >
        <div
          style={{
            position: "absolute",
            left: "50%",
            transform: "translateX(-50%)",
            bottom: 12,
            display: jumpVisible ? "flex" : "none",
            alignItems: "center",
            pointerEvents: "auto",
          }}
        >
          <ScrollToBottomButton
            label={t("session.jumpLatest")}
            onClick={jumpToLatest}
            testId="conversation-jump-latest"
          />
        </div>
      </div>

      {/* 输入框与对话流之间固定 16px；用 margin 而不是父层 gap，见容器上的注释。 */}
      <div style={{ marginTop: 16, minWidth: 0 }}>
        {disabled ? (
          <div
            data-testid="conversation-disabled"
            style={{
              fontSize: 12,
              color: "var(--text-secondary)",
              textAlign: "center",
              padding: "12px 0",
              borderTop: "1px solid var(--border-color)",
            }}
          >
            {t("task.activity.disabled")}
          </div>
        ) : pendingPrompt ? (
          // 有待回答的问题/审批时普通留言不能代替这个决定（D4），输入框整体换成提示。
          <div
            data-testid="conversation-prompt-pending"
            style={{
              fontSize: 12,
              color: "var(--status-warn, #d97706)",
              textAlign: "center",
              padding: "12px 0",
              borderTop: "1px solid var(--border-color)",
            }}
          >
            {pendingPrompt.type === "question"
              ? t("task.activity.questionPending")
              : t("task.activity.gatePending")}
          </div>
        ) : (
          <ActivityInput
            body={body}
            onBodyChange={setBody}
            rootId={rootId}
            nodeId={nodeId}
            choice={choice}
            versionId={versionId}
            onWorkflowChange={(nextChoice, nextVersionId) => {
              setChoice(nextChoice);
              setVersionId(nextVersionId);
            }}
            sending={sending}
            error={sendError}
            resetKey={composerResetKey}
            attachments={pendingAttachments}
            onAttachFiles={appendPendingAttachments}
            onRemoveAttachment={removePendingAttachment}
            uploadProgress={uploadProgress}
            onCancelUpload={cancelUpload}
            onSend={(text) => void handleSend(text)}
          />
        )}
      </div>

      {/* 滚动锚点：零高度、零 margin，只用来向上找滚动容器（见 conversationScroll.ts）。 */}
      <div ref={scrollAnchorRef} data-testid="conversation-scroll-anchor" aria-hidden style={{ height: 0 }} />
    </div>
  );
}