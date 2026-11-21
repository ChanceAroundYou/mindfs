import React, { useState } from "react";
import { useI18n } from "../../i18n/index";
import type { TaskActivity } from "../../shared/taskComments";
import { normalizeActivityStatus, parseTaskActivityPayload } from "../../shared/activityModel";

export type WorkflowPublishDecision = "approve" | "reject";

export type TaskWorkflowPublishCardProps = {
  activity: TaskActivity;
  deciding?: boolean;
  error?: string | null;
  onDecision?: (
    activity: TaskActivity,
    decision: WorkflowPublishDecision,
    note?: string,
  ) => void | Promise<void>;
};

/**
 * 「把这张图固化进目录」的待批准卡（W3 T2）。
 *
 * 批准后才会写控制面，所以卡片必须让人看清三件事：哪张图、哪个作用域、为什么。
 * 正文由后端生成（含节点数与理由），这里只负责按钮与附言。
 */
export function TaskWorkflowPublishCard({
  activity,
  deciding = false,
  error = null,
  onDecision,
}: TaskWorkflowPublishCardProps) {
  const { t } = useI18n();
  const [note, setNote] = useState("");
  const waiting = normalizeActivityStatus(activity.status) === "waiting_user";
  const payload = parseTaskActivityPayload(activity);
  const request = (payload?.request ?? {}) as Record<string, unknown>;
  const name = typeof request.name === "string" && request.name ? request.name : "";
  const scope = typeof request.scope === "string" ? request.scope : "";
  const trimmedNote = note.trim();

  return (
    <div style={cardStyle} data-testid="task-workflow-publish">
      <div style={titleStyle}>
        <span style={iconStyle}>⏸</span>
        <span>{waiting ? t("task.activity.publishWaiting") : t("task.activity.publishTitle")}</span>
        {name && <span style={nameStyle}>{name}</span>}
        {scope && (
          <span style={scopeStyle}>
            {scope === "global"
              ? t("task.activity.publishScopeGlobal")
              : t("task.activity.publishScopeProject")}
          </span>
        )}
      </div>
      {activity.body && <div style={bodyStyle}>{activity.body}</div>}
      {waiting ? (
        <>
          <label style={noteLabelStyle}>
            <span style={noteLabelTextStyle}>{t("task.activity.publishNoteLabel")}</span>
            <textarea
              value={note}
              onChange={(event) => setNote(event.target.value)}
              placeholder={t("task.activity.publishNotePlaceholder")}
              style={noteInputStyle}
              rows={2}
            />
          </label>
          <div style={actionsStyle}>
            <button
              type="button"
              disabled={deciding || !onDecision}
              onClick={() => void onDecision?.(activity, "approve", trimmedNote || undefined)}
              style={primaryButtonStyle}
            >
              {t("task.runTimeline.approve")}
            </button>
            <button
              type="button"
              disabled={deciding || !onDecision}
              onClick={() => void onDecision?.(activity, "reject")}
              style={dangerButtonStyle}
            >
              {t("task.runTimeline.reject")}
            </button>
          </div>
        </>
      ) : (
        <div style={resultStyle}>
          {normalizeActivityStatus(activity.status) === "cancelled"
            ? t("task.activity.publishRejected")
            : normalizeActivityStatus(activity.status) === "failed"
              ? t("task.activity.publishStale")
              : t("task.activity.publishApproved")}
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
  flexWrap: "wrap",
  fontSize: 12,
  fontWeight: 700,
  color: "var(--status-warn, #d97706)",
};

const iconStyle: React.CSSProperties = { fontSize: 13 };

const nameStyle: React.CSSProperties = {
  fontWeight: 600,
  padding: "0 6px",
  borderRadius: 999,
  background: "rgba(217, 119, 6, 0.12)",
  color: "var(--text-primary)",
};

const scopeStyle: React.CSSProperties = {
  fontSize: 11,
  fontWeight: 600,
  color: "var(--text-secondary)",
};

const bodyStyle: React.CSSProperties = {
  fontSize: 13,
  lineHeight: 1.5,
  color: "var(--text-color)",
  whiteSpace: "pre-wrap",
  wordBreak: "break-word",
};

const noteLabelStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 4,
};

const noteLabelTextStyle: React.CSSProperties = {
  fontSize: 11,
  fontWeight: 700,
  color: "var(--text-secondary)",
};

const noteInputStyle: React.CSSProperties = {
  width: "100%",
  boxSizing: "border-box",
  resize: "vertical",
  padding: "6px 8px",
  borderRadius: 6,
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-color)",
  fontSize: 12,
  lineHeight: 1.5,
  fontFamily: "inherit",
};

const resultStyle: React.CSSProperties = {
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

const errorStyle: React.CSSProperties = {
  fontSize: 12,
  color: "var(--status-bad, #dc2626)",
};
