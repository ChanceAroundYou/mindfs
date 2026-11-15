import React from "react";
import { useI18n } from "../../i18n/index";
import type { HumanGateDecision, TaskActivity } from "../../shared/taskComments";
import { normalizeActivityStatus, parseTaskActivityPayload } from "../../shared/activityModel";

export type TaskHumanGateCardProps = {
  activity: TaskActivity;
  /** 任务已暂停时只给「恢复」，不再给审批按钮。 */
  paused?: boolean;
  deciding?: boolean;
  error?: string | null;
  onDecision?: (activity: TaskActivity, decision: HumanGateDecision) => void | Promise<void>;
  onPause?: () => void | Promise<void>;
  onResume?: () => void | Promise<void>;
};

/**
 * HumanGate 审批卡。
 *
 * 审批只能走专用端点（D4），普通留言永远不能代替决定；因此这里刻意不提供
 * 自由文本输入，只给批准 / 驳回 / 停止。停止 = 暂停当前 Run，恢复后同一张卡
 * 仍在，避免用户为了暂停而被迫先做一个业务决定。
 */
export function TaskHumanGateCard({
  activity,
  paused = false,
  deciding = false,
  error = null,
  onDecision,
  onPause,
  onResume,
}: TaskHumanGateCardProps) {
  const { t } = useI18n();
  const payload = parseTaskActivityPayload(activity);
  const waiting = normalizeActivityStatus(activity.status) === "waiting_user";
  const prompt =
    `${payload.prompt || ""}`.trim() ||
    `${activity.body || ""}`.trim();
  const decision = `${payload.decision || ""}`.trim();
  const status = normalizeActivityStatus(activity.status);

  return (
    <div
      data-testid="task-human-gate"
      data-activity-id={activity.id}
      style={cardStyle}
    >
      <div style={titleStyle}>
        <span style={iconStyle}>⏸</span>
        <span>{waiting ? t("task.activity.gateWaiting") : t("task.activity.gateTitle")}</span>
      </div>
      {prompt && <div style={promptStyle}>{prompt}</div>}

      {!waiting && (
        <div style={resultStyle}>
          {status === "cancelled" || decision === "reject"
            ? t("task.activity.gateRejected")
            : decision === "approve"
              ? t("task.activity.gateApproved")
              : t("task.activity.status.completed")}
        </div>
      )}

      {waiting && paused && (
        <>
          <div style={hintStyle}>{t("task.activity.gatePaused")}</div>
          <div style={actionsStyle}>
            <button
              type="button"
              data-testid="task-human-gate-resume"
              disabled={deciding}
              onClick={() => void onResume?.()}
              style={primaryButtonStyle}
            >
              {t("taskGroup.resume")}
            </button>
          </div>
        </>
      )}

      {waiting && !paused && (
        <div style={actionsStyle}>
          <button
            type="button"
            data-testid="task-human-gate-approve"
            disabled={deciding || !onDecision}
            onClick={() => void onDecision?.(activity, "approve")}
            style={primaryButtonStyle}
          >
            {t("task.runTimeline.approve")}
          </button>
          <button
            type="button"
            data-testid="task-human-gate-reject"
            disabled={deciding || !onDecision}
            onClick={() => void onDecision?.(activity, "reject")}
            style={dangerButtonStyle}
          >
            {t("task.runTimeline.reject")}
          </button>
          <button
            type="button"
            data-testid="task-human-gate-stop"
            disabled={deciding || !onPause}
            onClick={() => void onPause?.()}
            style={secondaryButtonStyle}
          >
            {t("task.activity.gateStop")}
          </button>
        </div>
      )}

      {error && <div style={errorStyle}>{error}</div>}
    </div>
  );
}

const cardStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 8,
  padding: 12,
  borderRadius: 10,
  border: "1px solid var(--status-warn, #d97706)",
  background: "var(--panel-bg)",
};

const titleStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 6,
  fontSize: 12,
  fontWeight: 700,
  color: "var(--status-warn, #d97706)",
};

const iconStyle: React.CSSProperties = { fontSize: 13 };

const promptStyle: React.CSSProperties = {
  fontSize: 13,
  lineHeight: 1.5,
  color: "var(--text-color)",
  whiteSpace: "pre-wrap",
  wordBreak: "break-word",
};

const resultStyle: React.CSSProperties = {
  fontSize: 12,
  color: "var(--text-secondary)",
};

const hintStyle: React.CSSProperties = {
  fontSize: 12,
  color: "var(--text-secondary)",
};

const actionsStyle: React.CSSProperties = {
  display: "flex",
  gap: 8,
  flexWrap: "wrap",
};

const baseButtonStyle: React.CSSProperties = {
  fontSize: 12,
  fontWeight: 600,
  padding: "5px 12px",
  borderRadius: 6,
  cursor: "pointer",
};

const primaryButtonStyle: React.CSSProperties = {
  ...baseButtonStyle,
  border: "1px solid var(--status-ok, #16a34a)",
  background: "transparent",
  color: "var(--status-ok, #16a34a)",
};

const dangerButtonStyle: React.CSSProperties = {
  ...baseButtonStyle,
  border: "1px solid var(--status-bad, #dc2626)",
  background: "transparent",
  color: "var(--status-bad, #dc2626)",
};

const secondaryButtonStyle: React.CSSProperties = {
  ...baseButtonStyle,
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-secondary)",
};

const errorStyle: React.CSSProperties = {
  fontSize: 12,
  color: "var(--status-bad, #dc2626)",
};
