import React from "react";
import { useI18n } from "../../i18n/index";
import type { HumanGateDecision, TaskActivity } from "../../shared/taskComments";
import {
  activityDisplayText,
  isRunReport,
  normalizeActivityStatus,
} from "../../shared/activityModel";
import { workflowDisplayName } from "../../shared/workflowDisplay";
import { formatFullTime } from "../../shared/datetime";
import { formatTime } from "../session/sessionViewerFormat";
import { InlineTokenText } from "../common/InlineTokenText";
import { MarkdownViewer } from "../common/MarkdownViewer";
import { TaskHumanGateCard } from "./TaskHumanGateCard";
import { TaskQuestionCard, type TaskQuestionAnswers } from "./TaskQuestionCard";

export type ActivityMessageProps = {
  activity: TaskActivity;
  rootId?: string;
  /** workflow id → 友好名；页脚只用名字，不展示 `:vN` 版本号。 */
  workflowNames?: Record<string, string> | null;
  /** 任务已暂停：gate 卡只给恢复入口。 */
  paused?: boolean;
  /** 当前有在途的答题/审批；卡片按钮会被禁用。 */
  promptDeciding?: boolean;
  promptError?: string | null;
  onOpenSession?: (sessionKey: string) => void;
  onAnswerQuestion?: (
    activity: TaskActivity,
    answers: TaskQuestionAnswers,
  ) => void | Promise<void>;
  onGateDecision?: (
    activity: TaskActivity,
    decision: HumanGateDecision,
    note?: string,
  ) => void | Promise<void>;
  onPauseTask?: () => void | Promise<void>;
  onResumeTask?: () => void | Promise<void>;
  /** 排队留言上的「立即执行」：把这条留言交给 Router。 */
  onRunQueued?: (activity: TaskActivity) => void | Promise<void>;
  /** 正在派发排队留言：按钮禁用，避免双击。 */
  runningQueued?: boolean;
};

/**
 * created_at 在历史活动里可能缺失或非法；非法时返回 null，整块时间不渲染。
 * 直接 new Date(x).toISOString() 会在 Invalid Date 上抛 RangeError 打崩整个面板。
 */
function parseActivityTime(value: string | undefined): number | null {
  if (!value) return null;
  const ms = new Date(value).getTime();
  return Number.isNaN(ms) ? null : ms;
}

/**
 * 对话消息：与会话界面同一套视觉。
 *
 * - 用户留言：右侧灰底大圆角气泡，时间在气泡下方。
 * - Agent 回复：无框 Markdown 正文，状态/工作流/时间/会话入口收进一行页脚。
 * - 系统事件：居中灰字。
 *
 * 不再渲染「用户 / Agent」角色标签：左右位置和气泡形态已经表达了身份。
 */
export function ActivityMessage({
  activity,
  rootId,
  workflowNames,
  paused = false,
  promptDeciding = false,
  promptError = null,
  onOpenSession,
  onAnswerQuestion,
  onGateDecision,
  onPauseTask,
  onResumeTask,
  onRunQueued,
  runningQueued = false,
}: ActivityMessageProps) {
  const { t, locale } = useI18n();

  // question / human_gate 是专用卡：必须在 role 分支之前拦下，否则会被当成
  // 普通 system / agent 气泡，丢掉回答与审批入口。
  if (activity.type === "question") {
    return (
      <TaskQuestionCard
        activity={activity}
        rootId={rootId}
        onAnswer={onAnswerQuestion}
      />
    );
  }
  if (activity.type === "human_gate") {
    const waiting = normalizeActivityStatus(activity.status) === "waiting_user";
    return (
      <TaskHumanGateCard
        activity={activity}
        paused={paused}
        deciding={promptDeciding}
        error={waiting ? promptError : null}
        onDecision={onGateDecision}
        onPause={onPauseTask}
        onResume={onResumeTask}
      />
    );
  }

  // 分类只认 role。type 只区分「用户留言」与「Agent 回复」两个特例，
  // attention / merge_conflict 这类系统活动同样带 role=system，
  // 按 type 白名单判断会把它们渲染成 Agent 正文。
  const role = activity.role === "agent" || activity.role === "system" ? activity.role : "user";
  const isUser = role === "user";
  const isSystem = role === "system";

  const displayText = activityDisplayText(activity);
  const status = activity.status ? normalizeActivityStatus(activity.status) : null;
  // 状态不再逐条展示：任务级状态在详情面板头部，消息页脚只留工作流/时间/会话入口。
  // 这里保留 status 仅用于两件事：给进行中的空回复补本地化占位，以及让终态空正文保持安静。
  const bodyText =
    displayText ||
    (!isUser && status === "pending"
      ? t("task.activity.replyPending")
      : !isUser && status === "running"
        ? t("task.activity.replyRunning")
        : "");
  const canOpenSession = !!activity.session_key && !!onOpenSession;
  const created = parseActivityTime(activity.created_at);
  const timeLabel = created !== null ? formatTime(activity.created_at, locale) : "";
  const fullTime = created !== null ? formatFullTime(created, locale) : undefined;
  const workflowName = workflowDisplayName(activity.workflow_version_id, workflowNames);

  // 系统消息：居中显示，样式特殊
  if (isSystem) {
    return (
      <div
        data-testid="activity-system"
        data-activity-id={activity.id}
        style={systemMessageStyle}
      >
        <div style={systemTextStyle}>{displayText}</div>
        {created !== null && (
          <time
            dateTime={activity.created_at}
            title={fullTime}
            style={systemTimeStyle}
          >
            {timeLabel}
          </time>
        )}
      </div>
    );
  }

  if (isUser) {
    return (
      <div
        data-testid="activity-user"
        data-activity-id={activity.id}
        style={userRowStyle}
      >
        {bodyText && (
          <div style={userBubbleStyle}>
            <InlineTokenText content={bodyText} variant="inverse" />
          </div>
        )}
        {created !== null && (
          <div style={userMetaRowStyle}>
            {activity.status === "queued" ? (
              <>
                <span style={queuedBadgeStyle}>{t("task.queued")}</span>
                {onRunQueued ? (
                  <button
                    type="button"
                    onClick={() => void onRunQueued(activity)}
                    disabled={runningQueued}
                    style={runQueuedButtonStyle(runningQueued)}
                  >
                    {runningQueued ? t("task.startingNow") : t("task.startImmediately")}
                  </button>
                ) : null}
              </>
            ) : null}
            <time
              dateTime={activity.created_at}
              title={fullTime}
              style={userTimeStyle}
            >
              {timeLabel}
            </time>
          </div>
        )}
      </div>
    );
  }

  return (
    <div
      data-testid="activity-agent"
      data-activity-id={activity.id}
      style={agentMessageStyle}
    >
      {bodyText ? (
        <MarkdownViewer content={bodyText} root={rootId || undefined} />
      ) : null}

      <div data-testid="activity-footer" style={footerStyle}>
        {isRunReport(activity) && (
          <span data-testid="activity-report-tag" style={reportTagStyle}>
            {t("task.activity.reportTag")}
          </span>
        )}
        {workflowName && <span style={footerItemStyle}>{workflowName}</span>}
        {timeLabel && (
          <time dateTime={activity.created_at} title={fullTime} style={footerItemStyle}>
            {timeLabel}
          </time>
        )}
        {canOpenSession && (
          <button
            type="button"
            data-testid="activity-open-session"
            onClick={() => onOpenSession?.(activity.session_key as string)}
            style={footerButtonStyle}
          >
            {t("task.activity.openSession")}
          </button>
        )}
      </div>
    </div>
  );
}

// ========== Styles ==========

const userRowStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  alignItems: "flex-end",
  gap: 4,
  width: "100%",
};

const userBubbleStyle: React.CSSProperties = {
  padding: "10px 16px",
  borderRadius: "18px 18px 4px 18px",
  background: "rgba(148,163,184,0.14)",
  color: "var(--text-primary)",
  fontSize: 14,
  lineHeight: 1.5,
  whiteSpace: "pre-wrap",
  overflowWrap: "anywhere",
  wordBreak: "break-word",
  maxWidth: "min(80%, 640px)",
  minWidth: 0,
};

const userTimeStyle: React.CSSProperties = {
  fontSize: 10,
  color: "var(--text-secondary)",
  opacity: 0.5,
};

const userMetaRowStyle: React.CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  gap: 6,
};

const queuedBadgeStyle: React.CSSProperties = {
  fontSize: 10,
  fontWeight: 700,
  color: "var(--text-secondary)",
  border: "1px solid var(--border-color)",
  borderRadius: 999,
  padding: "1px 6px",
};

/** 排队留言上的「立即执行」：小胶囊，和页脚的其它元信息同一档字号。 */
function runQueuedButtonStyle(disabled: boolean): React.CSSProperties {
  return {
    fontSize: 11,
    fontWeight: 700,
    padding: "2px 8px",
    borderRadius: 999,
    border: "1px solid var(--accent-color)",
    background: "rgba(37, 99, 235, 0.10)",
    color: "var(--accent-color)",
    cursor: disabled ? "not-allowed" : "pointer",
    opacity: disabled ? 0.6 : 1,
  };
}

const agentMessageStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 2,
  width: "100%",
  minWidth: 0,
};

const footerStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  flexWrap: "wrap",
  gap: "2px 8px",
  fontSize: 10,
  color: "var(--text-secondary)",
  opacity: 0.5,
  minWidth: 0,
};

const footerItemStyle: React.CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  minWidth: 0,
  overflowWrap: "anywhere",
  fontVariantNumeric: "tabular-nums",
};

/**
 * 进度标记：浅色描边胶囊，只打在 Router 运行期 `purpose=progress` 的回复上
 * （不是消息类型），用来和同一轮的最终结果区分。
 *
 * 页脚整体 opacity 0.5，这里单独恢复不透明度，否则标记几乎看不见，
 * 用户又分不清这条是结算结果还是过程上报。
 */
const reportTagStyle: React.CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  padding: "0 6px",
  borderRadius: 999,
  border: "1px solid var(--border-color)",
  fontSize: 9,
  fontWeight: 700,
  lineHeight: "14px",
  color: "var(--text-secondary)",
  opacity: 1,
};

const footerButtonStyle: React.CSSProperties = {
  border: "none",
  background: "transparent",
  padding: 0,
  color: "var(--accent-color)",
  fontSize: 10,
  cursor: "pointer",
  flexShrink: 0,
};

const systemMessageStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  alignItems: "center",
  gap: 4,
  padding: "8px 0",
};

const systemTextStyle: React.CSSProperties = {
  fontSize: 12,
  color: "var(--text-secondary)",
  textAlign: "center",
};

const systemTimeStyle: React.CSSProperties = {
  fontSize: 10,
  color: "var(--text-secondary)",
  opacity: 0.7,
};
