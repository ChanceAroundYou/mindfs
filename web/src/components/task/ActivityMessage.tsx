import type React from "react";
import { useI18n } from "../../i18n/index";
import type { TaskActivity } from "../../shared/taskComments";
import {
  activityDisplayText,
  normalizeActivityStatus,
} from "../../shared/activityModel";

export type ActivityMessageProps = {
  activity: TaskActivity;
  onOpenSession?: (sessionKey: string) => void;
  onViewRun?: (runId: string) => void;
};

// i18n key 必须是字面量，模板字符串会丢失 MessageKey 的类型约束。
const STATUS_LABEL_KEYS = {
  pending: "task.activity.status.pending",
  running: "task.activity.status.running",
  completed: "task.activity.status.completed",
  failed: "task.activity.status.failed",
  cancelled: "task.activity.status.cancelled",
} as const;

const STATUS_COLORS: Record<string, string> = {
  pending: "var(--text-secondary)",
  running: "var(--accent-color)",
  completed: "var(--status-ok, #16a34a)",
  failed: "#dc2626",
  cancelled: "var(--text-secondary)",
};

function formatTime(value: string | undefined): string {
  if (!value) return "";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "" : date.toLocaleString();
}

/**
 * 单条对话消息。
 *
 * 状态显示是这一层的核心（Review Focus：Activity 状态必须如实反映执行结果）：
 * pending/running 展示实时正文（后端把「步骤 i/n」写进 body），completed 展示总结，
 * failed 用红色错误文案，cancelled 保持中性 —— 任何状态都不能被伪装成成功。
 */
export function ActivityMessage({ activity, onOpenSession, onViewRun }: ActivityMessageProps) {
  const { t } = useI18n();
  const role = activity.role === "agent" || activity.role === "system" ? activity.role : "user";

  if (role === "system") {
    return (
      <div
        data-testid="activity-message"
        data-role="system"
        style={{ alignSelf: "center", maxWidth: "90%", fontSize: 11, color: "var(--text-secondary)" }}
      >
        {activityDisplayText(activity)}
      </div>
    );
  }

  const isUser = role === "user";
  const status = normalizeActivityStatus(activity.status);
  const statusColor = STATUS_COLORS[status] || STATUS_COLORS.pending;
  const text = activityDisplayText(activity);
  const author = isUser ? (activity.author || t("task.activity.user")) : t("task.activity.agent");
  const time = formatTime(activity.updated_at || activity.created_at);
  const canViewRun = !isUser && !!activity.run_id;
  const canOpenSession = !isUser && !!activity.session_key;

  return (
    <div
      data-testid="activity-message"
      data-role={role}
      {...(isUser ? {} : { "data-status": status })}
      style={{
        alignSelf: isUser ? "flex-end" : "flex-start",
        maxWidth: "85%",
        border: "1px solid var(--border-color)",
        borderRadius: 8,
        padding: 10,
        background: "var(--content-bg)",
      }}
    >
      <div style={{ display: "flex", gap: 6, alignItems: "center", marginBottom: 4, flexWrap: "wrap" }}>
        <span
          style={{
            fontSize: 11,
            fontWeight: 700,
            color: isUser ? "var(--text-color)" : "var(--accent-color)",
          }}
        >
          {author}
        </span>
        {!isUser && (
          <span
            data-testid="activity-status"
            style={{
              fontSize: 10,
              fontWeight: 700,
              color: statusColor,
              border: `1px solid ${statusColor}`,
              borderRadius: 4,
              padding: "0 4px",
            }}
          >
            {t(STATUS_LABEL_KEYS[status])}
          </span>
        )}
        {time && <span style={{ fontSize: 10, color: "var(--text-secondary)" }}>{time}</span>}
      </div>

      <div
        style={{
          fontSize: 12,
          whiteSpace: "pre-wrap",
          overflowWrap: "anywhere",
          color: !isUser && status === "failed" ? "#dc2626" : "var(--text-color)",
        }}
      >
        {text}
      </div>

      {(canViewRun || canOpenSession) && (
        <div style={{ display: "flex", gap: 6, marginTop: 8, flexWrap: "wrap" }}>
          {canViewRun && (
            <button
              type="button"
              data-testid="activity-view-run"
              onClick={() => onViewRun?.(activity.run_id as string)}
              style={actionStyle}
            >
              {t("task.activity.viewRun")} →
            </button>
          )}
          {canOpenSession && (
            <button
              type="button"
              data-testid="activity-open-session"
              onClick={() => onOpenSession?.(activity.session_key as string)}
              style={actionStyle}
            >
              {t("task.activity.openSession")}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

const actionStyle: React.CSSProperties = {
  fontSize: 11,
  padding: "4px 8px",
  borderRadius: 4,
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "inherit",
  cursor: "pointer",
};