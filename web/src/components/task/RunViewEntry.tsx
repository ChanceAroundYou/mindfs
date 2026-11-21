import React from "react";
import { taskStatusColor, taskStatusLabel } from "../../app/appTask";
import { useI18n } from "../../i18n/index";
import { SessionTypeIcon } from "../common/SessionTypeIcon";
import { RunTimeline } from "./RunTimeline";
import { runHistoryGroups } from "./runTimelineModel";
import { useRunViewEntry } from "./useRunViewEntry";

// 执行详情面板：由 TaskInspectEntries 的胶囊按钮展开，本身不再画按钮。
//
// 展开后直接是简化的节点结果，不再提供「返回留言板」——再点一次胶囊即可收起。
//
// 一个任务可能跑过很多轮（每条留言在 Router 决定走 workflow 后都可能新建一条
// WorkflowRun），而引擎只绑定最新一条。所以这里按轮次逐块展示，旧的在前；
// 只有一轮时不打「第 N 轮」标签，免得单轮任务凭空多一行标题。
//
// 直连对话任务没有 WorkflowRun，但直连本身就是一种节点：这里按同样的节点行
// 展示「直连对话」及其任务状态，会话入口跳任务主会话，不额外写提示文案。

export type RunViewEntryProps = {
  rootId: string;
  taskId: string;
  nodeId?: string;
  /** 任务头每次更新递增（父层传 task.updated_at）：引擎绑定可能在对话回合里变。 */
  refreshKey?: string;
  /** 由同一行的胶囊组控制；false 时面板不渲染内容（hook 仍挂载以保留状态）。 */
  open: boolean;
  onOpenSession: (sessionKey: string) => void;
  /** 直连对话任务（`engine=conversation`）的执行会话就是任务主会话。 */
  fallbackSessionKey?: string;
  /** 任务状态：直连节点行沿用任务级状态，与详情面板标题同源。 */
  taskStatus?: string;
};

export function RunViewEntry({
  rootId,
  taskId,
  nodeId,
  refreshKey,
  open,
  onOpenSession,
  fallbackSessionKey,
  taskStatus,
}: RunViewEntryProps) {
  const { t } = useI18n();
  const { runId, engineReady, runs, loading, error, gateError, decidingNodeRunId, decideGate } =
    useRunViewEntry(rootId, taskId, nodeId, refreshKey, open);

  if (!open) return null;

  // 引擎探测未完成时先显示加载，否则直连节点行会先闪一下再换成运行视图。
  if (!engineReady) {
    return (
      <div id="task-run-panel" data-testid="run-view-entry" style={panelStyle}>
        <div style={hintStyle}>{t("task.runTimeline.loading")}</div>
      </div>
    );
  }

  if (!runId) {
    const status = taskStatus || "";
    return (
      <div id="task-run-panel" data-testid="run-view-entry" data-run-view-mode="direct" style={panelStyle}>
        <div data-testid="direct-node-run" style={nodeRowStyle}>
          <div style={nodeHeaderStyle}>
            <span style={{ ...nodeDotStyle, background: taskStatusColor(status) }} aria-hidden />
            <span style={nodeNameStyle}>{t("task.runTimeline.directNode")}</span>
            {status ? (
              <span style={{ ...statusStyle, color: taskStatusColor(status) }}>
                {taskStatusLabel(status, t)}
              </span>
            ) : null}
            {fallbackSessionKey ? (
              <button
                type="button"
                onClick={() => onOpenSession(fallbackSessionKey)}
                data-testid="direct-node-open-session"
                title={t("task.runTimeline.openSession")}
                aria-label={t("task.runTimeline.openSession")}
                style={sessionButtonStyle}
              >
                <SessionTypeIcon type="task" agent="" />
              </button>
            ) : null}
          </div>
        </div>
      </div>
    );
  }

  const groups = runHistoryGroups(runs);

  return (
    <div id="task-run-panel" data-testid="run-view-entry" style={panelStyle}>
      {error ? <div style={{ fontSize: "12px", color: "var(--danger-color, #ef4444)" }}>{error}</div> : null}
      {gateError ? (
        <div data-testid="human-gate-error" role="alert" style={{ fontSize: "12px", color: "var(--danger-color, #ef4444)" }}>
          {t("task.runTimeline.gateFailed")}: {gateError}
        </div>
      ) : null}
      {runs !== null ? (
        groups.length === 0 ? (
          <RunTimeline nodeRuns={[]} />
        ) : (
          <div style={{ display: "flex", flexDirection: "column", gap: "8px" }}>
            {groups.map((group) => (
              <div key={group.run.id} data-testid={`run-round-${group.round}`}>
                {groups.length > 1 ? (
                  <div style={{ fontSize: 11, color: "var(--text-secondary)", marginBottom: 2 }}>
                    {t("task.runTimeline.roundLabel", { n: group.round })}
                  </div>
                ) : null}
                <RunTimeline
                  nodeRuns={group.nodeRuns}
                  nodeNames={group.nodeNames}
                  nodeKinds={group.nodeKinds}
                  onOpenSession={onOpenSession}
                  decidingNodeRunId={decidingNodeRunId}
                  onDecideGate={(nodeRun, decision) => void decideGate(nodeRun, decision)}
                />
              </div>
            ))}
          </div>
        )
      ) : !error && loading ? (
        <div style={hintStyle}>{t("task.runTimeline.loading")}</div>
      ) : null}
    </div>
  );
}

const panelStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "6px",
};

const hintStyle: React.CSSProperties = {
  fontSize: "11px",
  color: "var(--text-secondary)",
};

const nodeRowStyle: React.CSSProperties = {
  border: "1px solid var(--border-color)",
  borderRadius: "8px",
  background: "rgba(0,0,0,0.02)",
  padding: "8px 10px",
  minWidth: 0,
};

const nodeHeaderStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: "6px",
  minWidth: 0,
};

const nodeDotStyle: React.CSSProperties = {
  width: 8,
  height: 8,
  borderRadius: "50%",
  flexShrink: 0,
};

const nodeNameStyle: React.CSSProperties = {
  flex: "1 1 auto",
  minWidth: 0,
  fontSize: 12,
  fontWeight: 700,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

const statusStyle: React.CSSProperties = {
  fontSize: 11,
  fontWeight: 700,
  flexShrink: 0,
};

const sessionButtonStyle: React.CSSProperties = {
  marginLeft: "auto",
  border: "none",
  background: "transparent",
  padding: 0,
  cursor: "pointer",
  display: "inline-flex",
  alignItems: "center",
  flexShrink: 0,
};
