import { useEffect, useRef, useState } from "react";
import { useI18n } from "../../i18n/index";
import { useTaskActivities } from "../../hooks/useTaskActivities";
import type { TaskActivity, WorkflowChoice } from "../../shared/taskComments";
import { pendingTaskPrompt } from "../../shared/activityModel";
import { ActivityInput } from "./ActivityInput";
import { ActivityMessage } from "./ActivityMessage";
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

  return (
    <div
      data-testid="conversation-messages"
      style={{ display: "flex", flexDirection: "column", gap: 10 }}
    >
      {activities.map((activity) => (
        <ActivityMessage
          key={activity.id}
          activity={activity}
          rootId={rootId}
          workflowNames={workflowNames}
          paused={paused}
          promptDeciding={promptDeciding}
          promptError={promptError}
          onOpenSession={onOpenSession}
          onAnswerQuestion={onAnswerQuestion}
          onGateDecision={onGateDecision}
          onPauseTask={onPauseTask}
          onResumeTask={onResumeTask}
          onRunQueued={onRunQueued}
          runningQueued={runningQueued}
        />
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
  const workflowNames = useWorkflowNames();
  const pendingPrompt = pendingTaskPrompt(activities);
  const lastAgentReplyRef = useRef("");

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

  const handleSend = async (text: string) => {
    try {
      await send(text, choice, versionId);
      setBody("");
      setChoice("empty");
      setVersionId("");
      // PromptEditor 内部持有文本；父层清空后必须换一个 resetKey 才会真正清空编辑器。
      setComposerResetKey((key) => key + 1);
    } catch {
      // 错误已由 hook 写入 sendError；草稿保留，用户改完可以直接重发。
    }
  };

  return (
    <div
      data-testid="conversation-view"
      style={{ display: "flex", flexDirection: "column", gap: 16 }}
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
          choice={choice}
          versionId={versionId}
          onWorkflowChange={(nextChoice, nextVersionId) => {
            setChoice(nextChoice);
            setVersionId(nextVersionId);
          }}
          sending={sending}
          error={sendError}
          resetKey={composerResetKey}
          onSend={(text) => void handleSend(text)}
        />
      )}
    </div>
  );
}