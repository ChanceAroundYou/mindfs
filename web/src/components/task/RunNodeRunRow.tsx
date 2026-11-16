import React from "react";
import { useI18n } from "../../i18n/index";
import { sessionIconButtonStyle } from "./taskDetailStyles";
import type { NodeRunSnapshot } from "./runTimelineTypes";
import { nodeRunStatusColor, nodeRunStatusLabel } from "./runTimelineModel";
import { readablePayload } from "./runTimelinePayload";
import { formatFullTime } from "../../shared/datetime";

// 运行时间线里的单个 NodeRun 行。
//
// 纯展示：输入/输出/错误只读渲染，session 跳转与 HumanGate 决定都通过回调交给
// 父层。HumanGate 的两个按钮只在父层判定 isGate（节点类型 human_gate 且状态
// waiting_user）时出现；deciding 时双禁用，避免一次点击发出两个决定。
//
// 输入/输出走 readablePayload：给用户看摘要、变更清单和验证结果；原始 JSON
// 收在 details 里，需要排查时再展开。样式与对话气泡共用同一套圆角/字号/间距，
// 避免「留言板是卡片、运行视图是 JSON 转储」的割裂感。

function formatTime(value: string | undefined, locale: string): string | null {
  if (!value) return null;
  const ms = new Date(value).getTime();
  return Number.isNaN(ms) ? value : formatFullTime(ms, locale);
}

export type RunNodeRunRowProps = {
  nodeRun: NodeRunSnapshot;
  displayName: string;
  isGate: boolean;
  deciding: boolean;
  onOpenSession?: (sessionKey: string) => void;
  onDecideGate?: (nodeRun: NodeRunSnapshot, decision: "approve" | "reject") => void;
};

export function RunNodeRunRow({ nodeRun, displayName, isGate, deciding, onOpenSession, onDecideGate }: RunNodeRunRowProps) {
  const { t, locale } = useI18n();
  const input = readablePayload(nodeRun.input);
  const output = readablePayload(nodeRun.output);
  const errorText = nodeRun.error?.trim() || null;
  const started = formatTime(nodeRun.started_at, locale);
  const finished = formatTime(nodeRun.finished_at, locale);
  const hasInput = input.text !== "" || input.items.length > 0 || input.fields.length > 0;
  const hasOutput = output.text !== "" || output.items.length > 0 || output.fields.length > 0;

  return (
    <div
      data-testid={`node-run-${nodeRun.id}`}
      data-node-run-id={nodeRun.id}
      data-node-id={nodeRun.node_id}
      data-status={nodeRun.status}
      style={cardStyle}
    >
      <div style={headerStyle}>
        <span
          aria-hidden="true"
          style={{ ...statusDotStyle, background: nodeRunStatusColor(nodeRun.status) }}
        />
        <span style={titleStyle}>{displayName}</span>
        <span style={{ fontSize: 11, fontWeight: 700, color: nodeRunStatusColor(nodeRun.status) }}>
          {nodeRunStatusLabel(nodeRun.status, t)}
        </span>
        {nodeRun.agent ? (
          <span style={metaStyle}>{nodeRun.agent}{nodeRun.model ? ` · ${nodeRun.model}` : ""}</span>
        ) : null}
        <span style={headerRightStyle}>
          {started || finished ? (
            <span style={metaStyle}>
              {started ? t("task.runTimeline.started", { time: started }) : ""}{started && finished ? " · " : ""}{finished ? t("task.runTimeline.finished", { time: finished }) : ""}
            </span>
          ) : null}
          {nodeRun.session_key && onOpenSession ? (
            <button
              type="button"
              title={t("task.runTimeline.openSession")}
              aria-label={t("task.runTimeline.openSession")}
              data-testid={`open-session-${nodeRun.id}`}
              onClick={() => onOpenSession(nodeRun.session_key as string)}
              style={sessionIconButtonStyle}
            >
              <span style={{ fontSize: 11 }}>{t("task.runTimeline.openSession")}</span>
            </button>
          ) : null}
        </span>
      </div>

      {isGate && onDecideGate ? (
        <div
          data-testid={`human-gate-actions-${nodeRun.id}`}
          style={{ display: "flex", alignItems: "center", gap: "6px", flexWrap: "wrap" }}
        >
          <span style={{ fontSize: "11px", fontWeight: 700, color: nodeRunStatusColor("waiting_user") }}>
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

      {hasInput ? (
        <div data-testid={`node-run-input-${nodeRun.id}`} style={sectionStyle}>
          <div style={sectionLabelStyle}>{t("task.runTimeline.input")}</div>
          <ReadableValue payload={input} />
          {input.raw ? (
            <details data-testid={`node-run-input-raw-${nodeRun.id}`} style={rawDetailsStyle}>
              <summary style={rawSummaryStyle}>{t("task.runTimeline.raw")}</summary>
              <pre style={rawPreStyle}>{input.raw}</pre>
            </details>
          ) : null}
        </div>
      ) : null}

      {hasOutput ? (
        <div data-testid={`node-run-output-${nodeRun.id}`} style={sectionStyle}>
          <div style={sectionLabelStyle}>{t("task.runTimeline.result")}</div>
          <ReadableValue payload={output} />
        </div>
      ) : null}
      {output.raw ? (
        <details data-testid={`node-run-raw-${nodeRun.id}`} style={rawDetailsStyle}>
          <summary style={rawSummaryStyle}>{t("task.runTimeline.raw")}</summary>
          <pre style={rawPreStyle}>{output.raw}</pre>
        </details>
      ) : null}

      {errorText ? (
        <div role="alert" style={sectionStyle}>
          <div style={{ ...sectionLabelStyle, color: nodeRunStatusColor("fail") }}>{t("task.runTimeline.error")}</div>
          <pre style={{ ...rawPreStyle, color: nodeRunStatusColor("fail") }}>{errorText}</pre>
        </div>
      ) : null}
    </div>
  );
}

function ReadableValue({ payload }: { payload: ReturnType<typeof readablePayload> }) {
  return (
    <>
      {payload.text ? <div style={bodyStyle}>{payload.text}</div> : null}
      {payload.items.length > 0 ? (
        <ul style={listStyle}>
          {payload.items.map((item, index) => (
            <li key={index}>{item}</li>
          ))}
        </ul>
      ) : null}
      {payload.fields.length > 0 ? (
        <div style={fieldListStyle}>
          {payload.fields.map((field) => (
            <div key={field.key} style={fieldRowStyle}>
              <span style={fieldKeyStyle}>{field.key}</span>
              <span style={fieldValueStyle}>{field.value}</span>
            </div>
          ))}
        </div>
      ) : null}
    </>
  );
}

// ========== Styles ==========

const cardStyle: React.CSSProperties = {
  border: "1px solid var(--border-color)",
  borderRadius: "10px",
  background: "var(--panel-bg)",
  padding: "12px",
  display: "flex",
  flexDirection: "column",
  gap: "8px",
};

const headerStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: "8px",
  flexWrap: "wrap",
};

const headerRightStyle: React.CSSProperties = {
  marginLeft: "auto",
  display: "inline-flex",
  gap: "4px",
  alignItems: "center",
};

const statusDotStyle: React.CSSProperties = {
  width: "8px",
  height: "8px",
  borderRadius: "999px",
  flexShrink: 0,
};

const titleStyle: React.CSSProperties = {
  fontWeight: 700,
  fontSize: 13,
  color: "var(--text-color)",
};

const metaStyle: React.CSSProperties = {
  fontSize: 11,
  color: "var(--text-secondary)",
};

const sectionStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "4px",
};

const sectionLabelStyle: React.CSSProperties = {
  fontSize: 11,
  fontWeight: 700,
  color: "var(--text-secondary)",
};

const bodyStyle: React.CSSProperties = {
  fontSize: 13,
  lineHeight: 1.5,
  color: "var(--text-color)",
  whiteSpace: "pre-wrap",
  wordBreak: "break-word",
};

const listStyle: React.CSSProperties = {
  margin: 0,
  paddingLeft: 18,
  fontSize: 13,
  lineHeight: 1.5,
  color: "var(--text-color)",
};

const fieldListStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: 2,
};

const fieldRowStyle: React.CSSProperties = {
  display: "flex",
  gap: 6,
  fontSize: 12,
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
  wordBreak: "break-word",
};

const rawDetailsStyle: React.CSSProperties = {
  marginTop: 2,
};

const rawSummaryStyle: React.CSSProperties = {
  fontSize: 11,
  color: "var(--accent-color)",
  cursor: "pointer",
};

const rawPreStyle: React.CSSProperties = {
  margin: "4px 0 0",
  whiteSpace: "pre-wrap",
  wordBreak: "break-word",
  fontSize: 11,
  fontFamily: "monospace",
  color: "var(--text-secondary)",
  background: "var(--content-bg)",
  borderRadius: 6,
  padding: 8,
};

const gateApproveStyle = (deciding: boolean): React.CSSProperties => ({
  border: "1px solid var(--status-ok)",
  background: "var(--status-ok)",
  color: "var(--panel-bg)",
  borderRadius: "6px",
  padding: "3px 10px",
  fontSize: "11px",
  fontWeight: 700,
  cursor: deciding ? "default" : "pointer",
  opacity: deciding ? 0.6 : 1,
});

const gateRejectStyle = (deciding: boolean): React.CSSProperties => ({
  border: "1px solid var(--status-bad)",
  background: "transparent",
  color: "var(--status-bad)",
  borderRadius: "6px",
  padding: "3px 10px",
  fontSize: "11px",
  fontWeight: 700,
  cursor: deciding ? "default" : "pointer",
  opacity: deciding ? 0.6 : 1,
});
