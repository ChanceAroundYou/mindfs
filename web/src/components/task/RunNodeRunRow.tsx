import React from "react";
import { useI18n } from "../../i18n/index";
import { sessionIconButtonStyle } from "./taskDetailStyles";
import type { NodeRunSnapshot } from "./runTimelineTypes";

// 运行时间线里的单个 NodeRun 行。
//
// 纯展示：输入/输出/错误只读渲染，session 跳转与 HumanGate 决定都通过回调交给
// 父层。HumanGate 的两个按钮只在父层判定 isGate（节点类型 human_gate 且状态
// waiting_user）时出现；deciding 时双禁用，避免一次点击发出两个决定。

function statusColor(status: string): string {
  switch (status) {
    case "success":
      return "var(--accent-color)";
    case "fail":
    case "cancelled":
      return "var(--danger-color, #ef4444)";
    case "running":
      return "var(--accent-color)";
    case "waiting_user":
      return "var(--warning-color, #f59e0b)";
    case "skipped":
    case "unknown":
    case "pending":
    case "queued":
    default:
      return "var(--text-secondary)";
  }
}

function formatPayload(value: Record<string, unknown> | string | undefined): string | null {
  if (value === undefined || value === null) return null;
  if (typeof value === "string") return value.trim() || null;
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function formatTime(value?: string): string | null {
  if (!value) return null;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
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
  const { t } = useI18n();
  const inputText = formatPayload(nodeRun.input);
  const outputText = formatPayload(nodeRun.output);
  const errorText = nodeRun.error?.trim() || null;
  const started = formatTime(nodeRun.started_at);
  const finished = formatTime(nodeRun.finished_at);

  return (
    <div
      data-testid={`node-run-${nodeRun.id}`}
      data-node-run-id={nodeRun.id}
      data-node-id={nodeRun.node_id}
      data-status={nodeRun.status}
      style={{
        border: "1px solid var(--border-color)",
        borderRadius: "8px",
        background: "var(--panel-bg)",
        padding: "10px",
        display: "flex",
        flexDirection: "column",
        gap: "6px",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: "8px", flexWrap: "wrap" }}>
        <span
          aria-hidden="true"
          style={{
            width: "8px",
            height: "8px",
            borderRadius: "999px",
            background: statusColor(nodeRun.status),
            flexShrink: 0,
          }}
        />
        <span style={{ fontWeight: 800, fontSize: "12px", color: "var(--text-color)" }}>{displayName}</span>
        <span style={{ fontSize: "11px", fontWeight: 700, color: statusColor(nodeRun.status) }}>{nodeRun.status}</span>
        {nodeRun.agent ? (
          <span style={{ fontSize: "11px", color: "var(--text-secondary)" }}>{nodeRun.agent}{nodeRun.model ? ` · ${nodeRun.model}` : ""}</span>
        ) : null}
        <span style={{ marginLeft: "auto", display: "inline-flex", gap: "4px", alignItems: "center" }}>
          {started || finished ? (
            <span style={{ fontSize: "11px", color: "var(--text-secondary)" }}>
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
              <span style={{ fontSize: "11px" }}>{t("task.runTimeline.openSession")}</span>
            </button>
          ) : null}
        </span>
      </div>

      {isGate && onDecideGate ? (
        <div
          data-testid={`human-gate-actions-${nodeRun.id}`}
          style={{ display: "flex", alignItems: "center", gap: "6px", flexWrap: "wrap" }}
        >
          <span style={{ fontSize: "11px", fontWeight: 700, color: statusColor("waiting_user") }}>
            {deciding ? t("task.runTimeline.deciding") : null}
          </span>
          <button
            type="button"
            data-testid={`human-gate-approve-${nodeRun.id}`}
            disabled={deciding}
            onClick={() => onDecideGate(nodeRun, "approve")}
            style={{
              border: "1px solid var(--accent-color)",
              background: "var(--accent-color)",
              color: "var(--panel-bg)",
              borderRadius: "6px",
              padding: "3px 10px",
              fontSize: "11px",
              fontWeight: 700,
              cursor: deciding ? "default" : "pointer",
              opacity: deciding ? 0.6 : 1,
            }}
          >
            {t("task.runTimeline.approve")}
          </button>
          <button
            type="button"
            data-testid={`human-gate-reject-${nodeRun.id}`}
            disabled={deciding}
            onClick={() => onDecideGate(nodeRun, "reject")}
            style={{
              border: "1px solid var(--danger-color, #ef4444)",
              background: "transparent",
              color: "var(--danger-color, #ef4444)",
              borderRadius: "6px",
              padding: "3px 10px",
              fontSize: "11px",
              fontWeight: 700,
              cursor: deciding ? "default" : "pointer",
              opacity: deciding ? 0.6 : 1,
            }}
          >
            {t("task.runTimeline.reject")}
          </button>
        </div>
      ) : null}

      {inputText ? (
        <div>
          <div style={{ fontSize: "11px", fontWeight: 700, color: "var(--text-secondary)" }}>{t("task.runTimeline.input")}</div>
          <pre style={{ margin: "2px 0 0", whiteSpace: "pre-wrap", wordBreak: "break-word", fontSize: "11px", color: "var(--text-color)" }}>{inputText}</pre>
        </div>
      ) : null}

      {outputText ? (
        <div>
          <div style={{ fontSize: "11px", fontWeight: 700, color: "var(--text-secondary)" }}>{t("task.runTimeline.output")}</div>
          <pre style={{ margin: "2px 0 0", whiteSpace: "pre-wrap", wordBreak: "break-word", fontSize: "11px", color: "var(--text-color)" }}>{outputText}</pre>
        </div>
      ) : null}

      {errorText ? (
        <div role="alert">
          <div style={{ fontSize: "11px", fontWeight: 700, color: statusColor("fail") }}>{t("task.runTimeline.error")}</div>
          <pre style={{ margin: "2px 0 0", whiteSpace: "pre-wrap", wordBreak: "break-word", fontSize: "11px", color: statusColor("fail") }}>{errorText}</pre>
        </div>
      ) : null}
    </div>
  );
}