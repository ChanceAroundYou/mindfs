import React from "react";
import { useI18n } from "../../i18n/index";
import type { HumanGateDecision, TaskActivity } from "../../shared/taskComments";
import {
  activityDisplayText,
  normalizeActivityStatus,
} from "../../shared/activityModel";
import { workflowDisplayName } from "../../shared/workflowDisplay";
import { formatFullTime } from "../../shared/datetime";
import { formatTime } from "../session/sessionViewerFormat";
import { InlineTokenText } from "../common/InlineTokenText";
import { ActivityTypeTag, activityMetaPillStyle } from "./ActivityTypeTag";
import { MarkdownViewer } from "../common/MarkdownViewer";
import { TaskHumanGateCard } from "./TaskHumanGateCard";
import { TaskWorkflowPublishCard, type WorkflowPublishDecision } from "./TaskWorkflowPublishCard";
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
  onWorkflowPublishDecision?: (
    activity: TaskActivity,
    decision: WorkflowPublishDecision,
    note?: string,
  ) => void | Promise<void>;
  onPauseTask?: () => void | Promise<void>;
  onResumeTask?: () => void | Promise<void>;
  /** 排队留言的「立即执行」：把最早一条 queued 留言交给 Router。 */
  onRunQueued?: (activity: TaskActivity) => void;
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
 * - 提醒（attention）：与 Agent 回复同一骨架，正文与胶囊按 severity 着色。
 * - 其他系统事件：居中灰字。
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
  onWorkflowPublishDecision,
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

  if (activity.type === "workflow_publish") {
    const waiting = normalizeActivityStatus(activity.status) === "waiting_user";
    return (
      <TaskWorkflowPublishCard
        activity={activity}
        deciding={promptDeciding}
        error={waiting ? promptError : null}
        onDecision={onWorkflowPublishDecision}
      />
    );
  }

  // 分类只认 role。type 只区分「用户留言」「Agent 回复」「提醒」三个特例，
  // merge_conflict 这类系统活动同样带 role=system，
  // 按 type 白名单判断会把它们渲染成 Agent 正文。
  const role = activity.role === "agent" || activity.role === "system" ? activity.role : "user";
  const isUser = role === "user";
  const isSystem = role === "system";
  // 提醒（attention）与 Agent 回复共用左对齐正文骨架：不居中、不套气泡。
  const isAttention = isSystem && activity.type === "attention";

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

  // 提醒：居左正文，和 Agent 回复同一骨架（无气泡、无内边距、同样的页脚）。
  // 正文与胶囊按 severity 着色：info=浅灰、warning=黄、error=红。
  if (isAttention) {
    const tone = attentionSeverityColor(activity.severity);
    return (
      <div
        data-testid="activity-system"
        data-activity-id={activity.id}
        style={agentMessageStyle}
      >
        {displayText ? (
          <div
            data-testid="activity-attention-body"
            data-severity={normalizeAttentionSeverity(activity.severity)}
            style={attentionBodyStyle(tone)}
          >
            {displayText}
          </div>
        ) : null}

        <div data-testid="activity-footer" style={footerStyle}>
          <ActivityTypeTag activity={activity} color={tone} />
          {timeLabel && (
            <time dateTime={activity.created_at} title={fullTime} style={footerItemStyle}>
              {timeLabel}
            </time>
          )}
        </div>
      </div>
    );
  }

  // 其他系统消息：居中显示，仍是中性一行。
  if (isSystem) {
    return (
      <div
        data-testid="activity-system"
        data-activity-id={activity.id}
        style={systemMessageStyle}
      >
        <div style={systemTextStyle}>{displayText}</div>
        {created !== null && (
          <div style={systemMetaRowStyle}>
            <ActivityTypeTag activity={activity} />
            <time
              dateTime={activity.created_at}
              title={fullTime}
              style={systemTimeStyle}
            >
              {timeLabel}
            </time>
          </div>
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
              // 「待执行」状态 + 就地派发入口：发送框里不再放「立即执行」，
              // 入口跟着排队这条留言走，只有真的排队时才出现。
              <>
                <span style={activityMetaPillStyle}>{t("task.queued")}</span>
                {onRunQueued ? (
                  <button
                    type="button"
                    data-testid="activity-run-queued"
                    disabled={runningQueued}
                    onMouseDown={(event) => event.preventDefault()}
                    onClick={() => onRunQueued(activity)}
                    style={queuedRunButtonStyle(runningQueued)}
                  >
                    {runningQueued ? t("task.startingNow") : t("task.startImmediately")}
                  </button>
                ) : null}
              </>
            ) : null}
            <ActivityTypeTag activity={activity} />
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
        <ActivityTypeTag activity={activity} />
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

/**
 * 三种消息（用户 / Agent / 系统）的元信息行共用一档：时间、工作流名、入口键都是
 * 10px + 次要色 + 半透明；胶囊恒为满不透明（见 activityMetaPillStyle）。
 * 行容器不挂 opacity —— 父级不透明度子级无法覆盖，整枚胶囊会跟着变浅。
 */
const metaTimeStyle: React.CSSProperties = {
  fontSize: 10,
  color: "var(--text-secondary)",
  opacity: 0.5,
};

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

/**
 * 提醒正文：和 Agent 回复同一骨架（左对齐、无气泡、无内边距），颜色按紧急程度一档，
 * 好和它提醒的内容区分开。色值只走现有 token，不新造主题色。
 */
function attentionBodyStyle(color: string): React.CSSProperties {
  return {
    color,
    minWidth: 0,
  };
}

/**
 * 提醒的紧急程度：后端只写 info/warning/error，空串与未知值都按 info
 * （旧数据没有 severity 列，不该被画成告警色）。
 */
export function normalizeAttentionSeverity(severity: string | undefined): "info" | "warning" | "error" {
  switch ((severity || "").trim().toLowerCase()) {
    case "warning":
      return "warning";
    case "error":
      return "error";
    default:
      return "info";
  }
}

/**
 * 紧急程度 → 颜色。info 沿用次要文字色；warning/error 用全局状态色，
 * 与任务状态同一套 token，不另造配色。
 */
export function attentionSeverityColor(severity: string | undefined): string {
  switch (normalizeAttentionSeverity(severity)) {
    case "warning":
      return "var(--status-warn)";
    case "error":
      return "var(--status-bad)";
    default:
      return "var(--text-secondary)";
  }
}

const userTimeStyle = metaTimeStyle;

const userMetaRowStyle: React.CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  flexWrap: "wrap",
  gap: 6,
};

function queuedRunButtonStyle(running: boolean): React.CSSProperties {
  return {
    fontSize: 10,
    fontWeight: 600,
    lineHeight: 1.5,
    padding: "1px 8px",
    borderRadius: 999,
    border: "1px solid var(--accent-color)",
    background: "transparent",
    color: "var(--accent-color)",
    cursor: running ? "default" : "pointer",
    opacity: running ? 0.6 : 1,
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
  minWidth: 0,
};

const footerItemStyle: React.CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  minWidth: 0,
  overflowWrap: "anywhere",
  fontVariantNumeric: "tabular-nums",
  opacity: 0.5,
};

const footerButtonStyle: React.CSSProperties = {
  border: "none",
  background: "transparent",
  padding: 0,
  color: "var(--accent-color)",
  fontSize: 10,
  cursor: "pointer",
  flexShrink: 0,
  opacity: 0.5,
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

const systemMetaRowStyle: React.CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  flexWrap: "wrap",
  gap: 6,
};

const systemTimeStyle = metaTimeStyle;
