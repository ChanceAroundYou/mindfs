import React, { useState } from "react";
import { useI18n } from "../../i18n/index";
import type { HumanGateDecision, TaskActivity } from "../../shared/taskComments";
import { normalizeActivityStatus, parseTaskActivityPayload } from "../../shared/activityModel";

export type TaskHumanGateCardProps = {
  activity: TaskActivity;
  /** 任务已暂停时只给「恢复」，不再给审批按钮。 */
  paused?: boolean;
  deciding?: boolean;
  error?: string | null;
  onDecision?: (
    activity: TaskActivity,
    decision: HumanGateDecision,
    note?: string,
  ) => void | Promise<void>;
  onPause?: () => void | Promise<void>;
  onResume?: () => void | Promise<void>;
};

/** payload.upstream 的单条：上游节点的可读产出（后端 gateActivityPayload 写入）。 */
type GateUpstream = { node_id?: string; name?: string; summary?: string };

function gateUpstream(value: unknown): GateUpstream[] {
  if (!Array.isArray(value)) return [];
  return value.filter((entry): entry is GateUpstream => {
    if (!entry || typeof entry !== "object") return false;
    const item = entry as GateUpstream;
    return typeof item.summary === "string" && item.summary.trim() !== "";
  });
}

/**
 * HumanGate 审批卡。
 *
 * 审批只能走专用端点（D4），普通留言永远不能代替决定；页面底部的留言框在
 * 等待审批时整体换成提示。卡片自己承担「说明在批什么」与「附言」：
 * - 标题带节点名（缺名字时用节点 id），正文是审批提示；
 * - 上游节点的可读产出摘要直接列出，用户不必去运行视图里拼上下文；
 * - 附言只随「批准」提交，交给 Router 驱动下一轮；驳回不会产生下一轮。
 *
 * 停止 = 暂停当前 Run，恢复后同一张卡仍在，避免用户为了暂停而被迫先做一个业务决定。
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
  const [note, setNote] = useState("");
  const payload = parseTaskActivityPayload(activity);
  const waiting = normalizeActivityStatus(activity.status) === "waiting_user";
  const prompt =
    `${payload.prompt || ""}`.trim() ||
    `${activity.body || ""}`.trim();
  const nodeName = `${payload.node_name || payload.node_id || ""}`.trim();
  const upstream = gateUpstream(payload.upstream);
  const autoApproved = payload.auto_approved === true;
  const decision = `${payload.decision || ""}`.trim();
  const status = normalizeActivityStatus(activity.status);
  const trimmedNote = note.trim();

  return (
    <div
      data-testid="task-human-gate"
      data-activity-id={activity.id}
      style={cardStyle}
    >
      <div style={titleStyle}>
        <span style={iconStyle}>⏸</span>
        <span>{waiting ? t("task.activity.gateWaiting") : t("task.activity.gateTitle")}</span>
        {nodeName && <span style={nodeNameStyle}>{nodeName}</span>}
      </div>
      {prompt && <div style={promptStyle}>{prompt}</div>}

      {upstream.length > 0 && (
        <div data-testid="task-human-gate-upstream" style={upstreamStyle}>
          <div style={upstreamLabelStyle}>{t("task.activity.gateUpstream")}</div>
          {upstream.map((item, index) => (
            <div key={`${item.node_id || "up"}:${index}`} style={upstreamItemStyle}>
              <span style={upstreamNameStyle}>{`${item.name || item.node_id || ""}`.trim()}</span>
              <span style={upstreamSummaryStyle}>{`${item.summary || ""}`.trim()}</span>
            </div>
          ))}
        </div>
      )}

      {!waiting && (
        <div style={resultStyle}>
          {autoApproved ? (
            <span data-testid="task-human-gate-auto">{t("task.activity.gateAutoApproved")}</span>
          ) : status === "cancelled" || decision === "reject" ? (
            t("task.activity.gateRejected")
          ) : decision === "approve" ? (
            t("task.activity.gateApproved")
          ) : (
            t("task.activity.status.completed")
          )}
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
        <>
          <label style={noteLabelStyle}>
            <span style={noteLabelTextStyle}>{t("task.activity.gateNoteLabel")}</span>
            <textarea
              data-testid="task-human-gate-note"
              value={note}
              onChange={(event) => setNote(event.target.value)}
              placeholder={t("task.activity.gateNotePlaceholder")}
              disabled={deciding}
              rows={2}
              style={noteInputStyle}
            />
          </label>
          <div style={actionsStyle}>
            <button
              type="button"
              data-testid="task-human-gate-approve"
              disabled={deciding || !onDecision}
              onClick={() =>
                void onDecision?.(activity, "approve", trimmedNote || undefined)
              }
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
        </>
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

/** 节点名跟在标题后面：用户第一眼要看到「哪个节点在拦我」。 */
const nodeNameStyle: React.CSSProperties = {
  fontWeight: 600,
  padding: "0 6px",
  borderRadius: 999,
  background: "rgba(217, 119, 6, 0.12)",
  color: "var(--text-primary)",
};

const promptStyle: React.CSSProperties = {
  fontSize: 13,
  lineHeight: 1.5,
  color: "var(--text-color)",
  whiteSpace: "pre-wrap",
  wordBreak: "break-word",
};

const upstreamStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 4,
  padding: "8px 10px",
  borderRadius: 8,
  background: "rgba(148,163,184,0.10)",
};

const upstreamLabelStyle: React.CSSProperties = {
  fontSize: 11,
  fontWeight: 700,
  color: "var(--text-secondary)",
};

const upstreamItemStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 2,
};

const upstreamNameStyle: React.CSSProperties = {
  fontSize: 11,
  fontWeight: 600,
  color: "var(--text-primary)",
};

/** 摘要最多三行：卡片是审批入口，不是结果阅读器；全文在运行视图里。 */
const upstreamSummaryStyle: React.CSSProperties = {
  fontSize: 12,
  lineHeight: 1.5,
  color: "var(--text-color)",
  whiteSpace: "pre-wrap",
  wordBreak: "break-word",
  display: "-webkit-box",
  WebkitLineClamp: 3,
  WebkitBoxOrient: "vertical",
  overflow: "hidden",
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

