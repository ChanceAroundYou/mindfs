import React from "react";
import { useI18n } from "../../i18n/index";
import { RunTimeline } from "./RunTimeline";
import { pickWorkflowSessionKey, runNodeKinds } from "./runTimelineModel";
import { useRunViewEntry } from "./useRunViewEntry";
import { WorkflowSessionJump } from "./WorkflowSessionJump";

// 运行视图入口：对话流下面的一条可展开行。
//
// 展开后直接是简化的节点结果，不再提供「返回留言板」——入口本身就在对话流里，
// 再点一次收起即可。

export type RunViewEntryProps = {
  rootId: string;
  taskId: string;
  nodeId?: string;
  /** 任务头每次更新递增（父层传 task.updated_at）：引擎绑定可能在对话回合里变。 */
  refreshKey?: string;
  onOpenSession: (sessionKey: string) => void;
};

export function RunViewEntry({ rootId, taskId, nodeId, refreshKey, onOpenSession }: RunViewEntryProps) {
  const { t } = useI18n();
  const { runId, open, run, nodeRuns, nodeRunsLoading, error, gateError, decidingNodeRunId, toggle, decideGate } =
    useRunViewEntry(rootId, taskId, nodeId, refreshKey);

  if (!runId) return null;

  const nodeNames: Record<string, string> = {};
  for (const node of run?.graph?.nodes ?? []) {
    if (node?.id) nodeNames[node.id] = node.name || node.id;
  }
  const nodeKinds = runNodeKinds(run?.graph);

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "6px", marginTop: "8px" }} data-testid="run-view-entry">
      <div style={{ display: "flex", alignItems: "center", gap: "6px" }}>
        <button
          type="button"
          onClick={toggle}
          data-testid="run-view-toggle"
          style={{
            alignSelf: "flex-start",
            border: "1px solid var(--border-color)",
            background: "transparent",
            color: "var(--text-secondary)",
            borderRadius: "6px",
            padding: "3px 8px",
            fontSize: "12px",
            cursor: "pointer",
          }}
        >
          {open ? t("task.runTimeline.collapse") : t("task.runTimeline.expand")}
        </button>
        <WorkflowSessionJump
          sessionKey={pickWorkflowSessionKey(nodeRuns)}
          loading={nodeRunsLoading}
          onOpenSession={onOpenSession}
        />
      </div>
      {open ? (
        <>
          {error ? <div style={{ fontSize: "12px", color: "var(--danger-color, #ef4444)" }}>{error}</div> : null}
          {gateError ? (
            <div data-testid="human-gate-error" role="alert" style={{ fontSize: "12px", color: "var(--danger-color, #ef4444)" }}>
              {t("task.runTimeline.gateFailed")}: {gateError}
            </div>
          ) : null}
          {run ? (
            <RunTimeline
              nodeRuns={nodeRuns}
              nodeNames={nodeNames}
              nodeKinds={nodeKinds}
              onOpenSession={onOpenSession}
              decidingNodeRunId={decidingNodeRunId}
              onDecideGate={(nodeRun, decision) => void decideGate(nodeRun, decision)}
            />
          ) : !error ? (
            <div style={{ fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.runTimeline.loading")}</div>
          ) : null}
        </>
      ) : null}
    </div>
  );
}
