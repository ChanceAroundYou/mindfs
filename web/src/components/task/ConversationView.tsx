import { useState } from "react";
import { useI18n } from "../../i18n/index";
import { useTaskActivities } from "../../hooks/useTaskActivities";
import type { TaskActivity, WorkflowChoice } from "../../shared/taskComments";
import { pendingTaskPrompt } from "../../shared/activityModel";
import { ActivityInput } from "./ActivityInput";
import { ActivityMessage } from "./ActivityMessage";

export type ConversationMessagesProps = {
  activities: TaskActivity[];
  rootId?: string;
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
};

/**
 * 纯展示的对话流。单独导出是为了能在 node 环境直接 SSR 测试 ——
 * 连接层的 ConversationView 会先经过 hook，effect 不跑时只能看到 loading。
 */
export function ConversationMessages({
  activities,
  rootId,
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
          paused={paused}
          promptDeciding={promptDeciding}
          promptError={promptError}
          onOpenSession={onOpenSession}
          onAnswerQuestion={onAnswerQuestion}
          onGateDecision={onGateDecision}
          onPauseTask={onPauseTask}
          onResumeTask={onResumeTask}
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
    promptDeciding,
    promptError,
  } = useTaskActivities(taskId, rootId, nodeId);
  const [body, setBody] = useState("");
  const [choice, setChoice] = useState<WorkflowChoice>("empty");
  const [versionId, setVersionId] = useState("");
  const pendingPrompt = pendingTaskPrompt(activities);

  const handleSend = async () => {
    try {
      await send(body, choice, versionId);
      setBody("");
      setChoice("empty");
      setVersionId("");
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
          onSend={() => void handleSend()}
        />
      )}
    </div>
  );
}