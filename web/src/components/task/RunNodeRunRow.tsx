import React from "react";
import { useI18n } from "../../i18n/index";
import type { NodeRunSnapshot } from "./runTimelineTypes";
import {
  formatNodeRunDuration,
  nodeRunDurationMs,
  nodeRunStatusColor,
  nodeRunStatusLabel,
} from "./runTimelineModel";
import { readablePayload } from "./runTimelinePayload";

// 运行时间线里的单个 NodeRun 行。
//
// 纯展示且刻意保持轻量：一行「状态点 + 节点名 + 状态 + 耗时 + 尝试次数 + 会话入口」，
// 下面是变更清单、验证字段、错误或人工审批按钮。
//
// 任务 #87 第 3 条：节点输出里的自然语言段落（summary/result/message…）就是
// Router 回复的原文，再抄一遍只是噪音 —— 详情去会话里看。运行视图只保留回复里
// 没有的信息：耗时、第几次尝试、改了哪些东西、验证结果、失败原因、会话入口。
// HumanGate 行的 prompt 例外，那是审批问题本身。

export type RunNodeRunRowProps = {
  nodeRun: NodeRunSnapshot;
  displayName: string;
  isGate: boolean;
  deciding: boolean;
  onOpenSession?: (sessionKey: string) => void;
  onDecideGate?: (nodeRun: NodeRunSnapshot, decision: "approve" | "reject") => void;
};

export function RunNodeRunRow({ nodeRun, displayName, isGate, deciding, onOpenSession, onDecideGate }: RunNodeRunRowProps) {
  const { t } = useI18n();
  const output = readablePayload(nodeRun.output, { gatePrompt: isGate });
  const errorText = nodeRun.error?.trim() || null;
  const hasOutput = output.text !== "" || output.items.length > 0 || output.fields.length > 0;
  const durationMs = nodeRunDurationMs(nodeRun);
  const durationText = durationMs === null ? "" : formatNodeRunDuration(durationMs, t);
  const showAttempt = (nodeRun.attempt ?? 1) > 1;

  return (
    <div
      data-testid={`node-run-${nodeRun.id}`}
      data-node-run-id={nodeRun.id}
      data-node-id={nodeRun.node_id}
      data-status={nodeRun.status}
      style={rowStyle}
    >
      <div style={headerStyle}>
        <span
          aria-hidden="true"
          style={{ ...statusDotStyle, background: nodeRunStatusColor(nodeRun.status) }}
        />
        <span style={titleStyle}>{displayName}</span>
        {durationText ? (
          <span data-testid={`node-run-duration-${nodeRun.id}`} style={metaStyle}>
            {durationText}
          </span>
        ) : null}
        {showAttempt ? (
          <span data-testid={`node-run-attempt-${nodeRun.id}`} style={metaStyle}>
            {t("task.runTimeline.attempt", { n: nodeRun.attempt })}
          </span>
        ) : null}
        <span style={{ fontSize: 11, color: nodeRunStatusColor(nodeRun.status), flexShrink: 0 }}>
          {nodeRunStatusLabel(nodeRun.status, t)}
        </span>
        {nodeRun.session_key && onOpenSession ? (
          <button
            type="button"
            title={t("task.runTimeline.openSession")}
            aria-label={t("task.runTimeline.openSession")}
            data-testid={`open-session-${nodeRun.id}`}
            onClick={() => onOpenSession(nodeRun.session_key as string)}
            style={sessionButtonStyle}
          >
            {t("task.runTimeline.openSession")}
          </button>
        ) : null}
      </div>

      {isGate && onDecideGate ? (
        <div
          data-testid={`human-gate-actions-${nodeRun.id}`}
          style={{ display: "flex", alignItems: "center", gap: "6px", flexWrap: "wrap" }}
        >
          <span style={{ fontSize: 11, color: nodeRunStatusColor("waiting_user") }}>
            {deciding ? t("task.runTimeline.deciding") : null}
          </span>
          <button
            type="button"
            data-testid={`human-gate-approve-${nodeRun.id}`}
            disabled={deciding}
            onClick={() => onDecideGate(nodeRun, "approve")}
            style={gateApproveStyle(deciding)}
          >
            {t("task.runTimeline.approve")}
          </button>
          <button
            type="button"
            data-testid={`human-gate-reject-${nodeRun.id}`}
            disabled={deciding}
            onClick={() => onDecideGate(nodeRun, "reject")}
            style={gateRejectStyle(deciding)}
          >
            {t("task.runTimeline.reject")}
          </button>
        </div>
      ) : null}

      {hasOutput ? (
        <div data-testid={`node-run-output-${nodeRun.id}`} style={resultStyle}>
          {output.text ? <div style={bodyStyle}>{output.text}</div> : null}
          {output.items.length > 0 ? (
            <ul style={listStyle}>
              {output.items.map((item, index) => (
                <li key={index}>{item}</li>
              ))}
            </ul>
          ) : null}
          {output.fields.length > 0 ? (
            <div style={fieldListStyle}>
              {output.fields.map((field) => (
                <div key={field.key} style={fieldRowStyle}>
                  <span style={fieldKeyStyle}>{field.key}</span>
                  <span style={fieldValueStyle}>{field.value}</span>
                </div>
              ))}
            </div>
          ) : null}
        </div>
      ) : null}

      {errorText ? (
        <div role="alert" style={{ ...bodyStyle, color: nodeRunStatusColor("fail") }}>{errorText}</div>
      ) : null}
    </div>
  );
}

// ========== Styles ==========

const rowStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 4,
  padding: "6px 0",
  minWidth: 0,
};

const headerStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 6,
  minWidth: 0,
};

const statusDotStyle: React.CSSProperties = {
  width: 7,
  height: 7,
  borderRadius: 999,
  flexShrink: 0,
};

const titleStyle: React.CSSProperties = {
  fontWeight: 600,
  fontSize: 13,
  color: "var(--text-color)",
  flex: "1 1 auto",
  minWidth: 0,
  overflowWrap: "anywhere",
};

const metaStyle: React.CSSProperties = {
  fontSize: 11,
  color: "var(--text-secondary)",
  flexShrink: 0,
  fontVariantNumeric: "tabular-nums",
};

const sessionButtonStyle: React.CSSProperties = {
  border: "none",
  background: "transparent",
  padding: 0,
  color: "var(--accent-color)",
  fontSize: 11,
  cursor: "pointer",
  flexShrink: 0,
};

const resultStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 3,
  paddingLeft: 13,
  minWidth: 0,
};

const bodyStyle: React.CSSProperties = {
  fontSize: 12.5,
  lineHeight: 1.5,
  color: "var(--text-color)",
  whiteSpace: "pre-wrap",
  overflowWrap: "anywhere",
  wordBreak: "break-word",
};

const listStyle: React.CSSProperties = {
  margin: 0,
  paddingLeft: 16,
  fontSize: 12.5,
  lineHeight: 1.5,
  color: "var(--text-color)",
};

const fieldListStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 1,
};

const fieldRowStyle: React.CSSProperties = {
  display: "flex",
  gap: 6,
  fontSize: 11.5,
  lineHeight: 1.5,
  minWidth: 0,
};

const fieldKeyStyle: React.CSSProperties = {
  flexShrink: 0,
  fontWeight: 600,
  color: "var(--text-secondary)",
};

const fieldValueStyle: React.CSSProperties = {
  color: "var(--text-color)",
  overflowWrap: "anywhere",
  wordBreak: "break-word",
};

const gateApproveStyle = (deciding: boolean): React.CSSProperties => ({
  border: "1px solid var(--status-ok)",
  background: "var(--status-ok)",
  color: "var(--panel-bg)",
  borderRadius: 6,
  padding: "3px 10px",
  fontSize: 11,
  fontWeight: 700,
  cursor: deciding ? "default" : "pointer",
  opacity: deciding ? 0.6 : 1,
});

const gateRejectStyle = (deciding: boolean): React.CSSProperties => ({
  border: "1px solid var(--status-bad)",
  background: "transparent",
  color: "var(--status-bad)",
  borderRadius: 6,
  padding: "3px 10px",
  fontSize: 11,
  fontWeight: 700,
  cursor: deciding ? "default" : "pointer",
  opacity: deciding ? 0.6 : 1,
});
