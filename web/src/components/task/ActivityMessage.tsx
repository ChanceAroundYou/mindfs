import React from "react";
import { useI18n } from "../../i18n/index";
import type { HumanGateDecision, TaskActivity } from "../../shared/taskComments";
import {
  activityDisplayText,
  normalizeActivityStatus,
} from "../../shared/activityModel";
import { formatRelativeTime, formatFullTime } from "../../shared/datetime";
import { TaskHumanGateCard } from "./TaskHumanGateCard";
import { TaskQuestionCard, type TaskQuestionAnswers } from "./TaskQuestionCard";

export type ActivityMessageProps = {
  activity: TaskActivity;
  rootId?: string;
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
  ) => void | Promise<void>;
  onPauseTask?: () => void | Promise<void>;
  onResumeTask?: () => void | Promise<void>;
};

// i18n key 必须是字面量，模板字符串会丢失 MessageKey 的类型约束。
const STATUS_LABEL_KEYS = {
  pending: "task.activity.status.pending",
  running: "task.activity.status.running",
  completed: "task.activity.status.completed",
  failed: "task.activity.status.failed",
  cancelled: "task.activity.status.cancelled",
  waiting_user: "task.activity.status.waiting_user",
} as const;

const STATUS_COLORS: Record<string, string> = {
  pending: "var(--text-secondary)",
  running: "var(--accent-color)",
  completed: "var(--status-ok)",
  failed: "var(--status-bad)",
  cancelled: "var(--text-secondary)",
  waiting_user: "var(--status-warn)",
};

const STATUS_ICONS: Record<string, string> = {
  pending: "⏳",
  running: "⚡",
  completed: "✓",
  failed: "✗",
  cancelled: "○",
  waiting_user: "⏸",
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
 * 对话消息气泡：用户消息右对齐蓝色，Agent回复左对齐灰色
 *
 * 设计理念：
 * 1. 明确的视觉区分 - 左右对齐 + 颜色差异
 * 2. 友好的时间显示 - 相对时间 + hover显示完整时间
 * 3. 内联状态显示 - 不破坏消息流
 * 4. 移动端友好 - 响应式宽度
 */
export function ActivityMessage({
  activity,
  rootId,
  paused = false,
  promptDeciding = false,
  promptError = null,
  onOpenSession,
  onAnswerQuestion,
  onGateDecision,
  onPauseTask,
  onResumeTask,
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
  // 按 type 白名单判断会把它们渲染成 Agent 气泡。
  const role = activity.role === "agent" || activity.role === "system" ? activity.role : "user";
  const isUser = role === "user";
  const isSystem = role === "system";

  const displayText = activityDisplayText(activity);
  const status = activity.status ? normalizeActivityStatus(activity.status) : null;
  // 后端不写中文占位：进行中的提示由前端本地化，终态空正文交给状态徽标，
  // 否则英文界面会混进「正在思考...」这种硬编码中文。
  const bodyText =
    displayText ||
    (!isUser && status === "pending"
      ? t("task.activity.replyPending")
      : !isUser && status === "running"
        ? t("task.activity.replyRunning")
        : "");
  const canOpenSession = !!activity.session_key && !!onOpenSession;
  const created = parseActivityTime(activity.created_at);

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
            title={formatFullTime(created, locale)}
            style={systemTimeStyle}
          >
            {formatRelativeTime(created, locale)}
          </time>
        )}
      </div>
    );
  }

  return (
    <div
      data-testid={isUser ? "activity-user" : "activity-agent"}
      data-activity-id={activity.id}
      style={containerStyle(isUser)}
    >
      <div style={bubbleStyle(isUser)}>
        {/* 角色标签 + 时间 */}
        <div style={headerStyle}>
          <span style={roleStyle(isUser)}>
            {isUser ? "👤" : "🤖"} {isUser ? t("task.activity.user") : t("task.activity.agent")}
          </span>
          {created !== null && (
            <time
              dateTime={activity.created_at}
              title={formatFullTime(created, locale)}
              style={timeStyle}
            >
              {formatRelativeTime(created, locale)}
            </time>
          )}
        </div>

        {/* 消息正文 */}
        {bodyText && <div style={bodyStyle}>{bodyText}</div>}

        {/* 工作流执行状态（内联显示） */}
        {!isUser && status && (
          <div style={statusBarStyle}>
            <span style={{ ...statusBadgeStyle, color: STATUS_COLORS[status] }}>
              {STATUS_ICONS[status]} {t(STATUS_LABEL_KEYS[status])}
            </span>
            {activity.workflow_version_id && (
              <span style={versionBadgeStyle}>
                {activity.workflow_version_id}
              </span>
            )}
          </div>
        )}

        {/* 操作按钮：运行详情就在本面板下方（RunViewEntry），不另设入口 */}
        {canOpenSession && (
          <div style={actionsStyle}>
            <button
              type="button"
              data-testid="activity-open-session"
              onClick={() => onOpenSession?.(activity.session_key as string)}
              style={actionButtonStyle}
            >
              {t("task.activity.openSession")}
            </button>
          </div>
        )}
      </div>
    </div>
  );
}

// ========== Styles ==========

const containerStyle = (isUser: boolean): React.CSSProperties => ({
  display: "flex",
  justifyContent: isUser ? "flex-end" : "flex-start",
  marginBottom: 12,
  width: "100%",
});

const bubbleStyle = (isUser: boolean): React.CSSProperties => ({
  maxWidth: "min(80%, 600px)",
  minWidth: "200px",
  padding: 12,
  borderRadius: 10,
  background: isUser
    ? "var(--accent-bg, rgba(59, 130, 246, 0.1))"
    : "var(--panel-bg)",
  border: isUser
    ? "1px solid var(--accent-color, #3b82f6)"
    : "1px solid var(--border-color)",
  display: "flex",
  flexDirection: "column",
  gap: 8,
});

const headerStyle: React.CSSProperties = {
  display: "flex",
  justifyContent: "space-between",
  alignItems: "center",
  gap: 8,
};

const roleStyle = (isUser: boolean): React.CSSProperties => ({
  fontSize: 12,
  fontWeight: 600,
  color: isUser ? "var(--accent-color, #3b82f6)" : "var(--text-color)",
  display: "flex",
  alignItems: "center",
  gap: 4,
});

const timeStyle: React.CSSProperties = {
  fontSize: 11,
  color: "var(--text-secondary)",
  flexShrink: 0,
};

const bodyStyle: React.CSSProperties = {
  fontSize: 13,
  lineHeight: 1.5,
  color: "var(--text-color)",
  whiteSpace: "pre-wrap",
  wordBreak: "break-word",
};

const statusBarStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 8,
  flexWrap: "wrap",
};

const statusBadgeStyle: React.CSSProperties = {
  fontSize: 11,
  fontWeight: 500,
  display: "inline-flex",
  alignItems: "center",
  gap: 4,
};

const versionBadgeStyle: React.CSSProperties = {
  fontSize: 10,
  padding: "2px 6px",
  borderRadius: 4,
  background: "var(--tag-bg, rgba(148, 163, 184, 0.2))",
  color: "var(--text-secondary)",
  fontFamily: "monospace",
};

const actionsStyle: React.CSSProperties = {
  display: "flex",
  gap: 8,
  flexWrap: "wrap",
};

const actionButtonStyle: React.CSSProperties = {
  fontSize: 11,
  padding: "4px 10px",
  borderRadius: 5,
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--accent-color)",
  cursor: "pointer",
  transition: "all 0.15s ease",
  fontWeight: 500,
};

const systemMessageStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  alignItems: "center",
  gap: 4,
  padding: "8px 0",
  marginBottom: 12,
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
