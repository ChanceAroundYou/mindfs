import React from "react";
import { useI18n } from "../../i18n/index";
import { sessionIconButtonStyle } from "./taskDetailStyles";

// WP-07 留言板下钻只读运行视图。
//
// 定位：留言板（TaskActivity / ContinueRequest）的一条留言在执行 workflow 时，
// 会产生一个 WorkflowRun 与其下的 NodeRun 序列。本组件是那条留言的**下钻子视图**——
// 只在留言板里点开某条留言后才出现，展示该留言触发的那次运行的节点时间线。
// 它不是留言板的平级 tab，也不是任务详情的平级 tab，不得直接挂到 TaskDetailPanel
// 的 tab 栏上（见 web/tests/run-timeline-drill-in.test.mjs 钉住的层级契约）。
//
// 只读：不发起任何写操作。输入/输出/错误仅展示；session 跳转通过回调交给父层
// （复用 TaskDetailPanel 的 onOpenSession 语义），本组件自己不做路由。
//
// 数据形状对齐后端 workflow_types.go（A5 `a571567d` 的 WorkflowRun/NodeRun）：
//   WorkflowRun { id, task_id, version?, revision, node_states, ... }
//   NodeRun { id, run_id, node_id, session_key?, status, input?, output?, started_at?, finished_at? }
// 后端目前还没有 NodeRun 的 HTTP 出口（WP-06 只落了 WorkflowRun 的编辑接口），
// 所以 run/nodeRuns 都是由父层传入的只读快照，父层负责取数；这一层不 import service。

export type WorkflowRunNodeState = "pending" | "queued" | "running" | "success" | "fail" | "waiting_user" | "skipped" | "unknown" | "cancelled";

export type WorkflowRunSnapshot = {
  id: string;
  task_id?: string;
  version?: string;
  revision?: number;
  node_states?: Record<string, string>;
  created_at?: string;
  updated_at?: string;
};

export type NodeRunSnapshot = {
  id: string;
  run_id?: string;
  node_id: string;
  session_key?: string;
  status: string;
  input?: Record<string, unknown> | string;
  output?: Record<string, unknown> | string;
  error?: string;
  started_at?: string;
  finished_at?: string;
  agent?: string;
  model?: string;
};

export type RunTimelineProps = {
  /** 本次下钻的 WorkflowRun 快照（留言板里点开的那条留言对应的运行）。 */
  run: WorkflowRunSnapshot | null | undefined;
  /** 该 run 下的 NodeRun 列表，按时间顺序（父层已排序）。 */
  nodeRuns?: NodeRunSnapshot[] | null;
  /** 节点 id → 显示名（来自 WorkflowRun.Graph 快照的节点名）；缺省时显示 node_id。 */
  nodeNames?: Record<string, string> | null;
  /** session 跳转：父层负责打开会话（复用 onOpenSession）。 */
  onOpenSession?: (sessionKey: string) => void;
  /** 回到留言板的回调：下钻视图的“返回”入口，证明层级是子视图而非平级 tab。 */
  onBackToComments?: () => void;
};

// 状态 → 提示色。颜色只用 CSS 变量，不写字面色值（workspaceStyles 的 token-only 纪律）。
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

function NodeRunRow({
  nodeRun,
  displayName,
  onOpenSession,
}: {
  nodeRun: NodeRunSnapshot;
  displayName: string;
  onOpenSession?: (sessionKey: string) => void;
}) {
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

export function RunTimeline({ run, nodeRuns, nodeNames, onOpenSession, onBackToComments }: RunTimelineProps) {
  const { t } = useI18n();
  const list: NodeRunSnapshot[] = Array.isArray(nodeRuns) ? nodeRuns : [];

  return (
    <div
      data-testid="run-timeline"
      data-drill-in="comment-run"
      style={{ display: "flex", flexDirection: "column", gap: "8px", padding: "4px 0" }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
        {onBackToComments ? (
          <button
            type="button"
            onClick={onBackToComments}
            data-testid="run-timeline-back"
            style={{
              border: "none",
              background: "transparent",
              color: "var(--accent-color)",
              cursor: "pointer",
              fontSize: "12px",
              fontWeight: 700,
              padding: 0,
            }}
          >
            {t("task.runTimeline.backToComments")}
          </button>
        ) : null}
        <span style={{ fontSize: "11px", color: "var(--text-secondary)" }}>
          {run ? `Run ${run.id}` : t("task.runTimeline.noRun")}
          {run?.version ? ` · ${run.version}` : ""}
          {typeof run?.revision === "number" ? ` · r${run.revision}` : ""}
        </span>
      </div>

      {list.length === 0 ? (
        <div style={{ fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.runTimeline.empty")}</div>
      ) : (
        <ol style={{ listStyle: "none", margin: 0, padding: 0, display: "flex", flexDirection: "column", gap: "8px" }}>
          {list.map((nodeRun) => (
            <li key={nodeRun.id}>
              <NodeRunRow nodeRun={nodeRun} displayName={nodeNames?.[nodeRun.node_id] ?? nodeRun.node_id} onOpenSession={onOpenSession} />
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}
