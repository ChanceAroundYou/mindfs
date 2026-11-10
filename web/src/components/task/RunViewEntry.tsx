import React from "react";
import { useI18n } from "../../i18n/index";
import { RunTimeline, type NodeRunSnapshot } from "./RunTimeline";
import { useRunViewEntry } from "./useRunViewEntry";

// 运行视图入口：挂在留言板**之下**的一条可展开行。
//
// 为什么做成独立组件：TaskDetailPanel 只加一行接线（避免它继续膨胀）。
// 层级仍是「留言板的下钻」——入口出现在留言板之后，展开后由 RunTimeline 自带
// 「返回留言板」按钮（data-drill-in="comment-run"）。
//
// 何时出现：只有任务被显式迁移到 WorkflowRun 引擎后才有运行视图可看；
// legacy 任务一概不渲染（引擎绑定由后端 /api/tasks/{id}/engine 决定）。
// 取数在 useRunViewEntry（.ts）里，视图层不 import services。

export type RunViewEntryProps = {
  rootId: string;
  taskId: string;
  nodeId?: string;
  onOpenSession: (sessionKey: string) => void;
};

export function RunViewEntry({ rootId, taskId, nodeId, onOpenSession }: RunViewEntryProps) {
  const { t } = useI18n();
  const { runId, open, run, nodeRuns, error, toggle, collapse } = useRunViewEntry(rootId, taskId, nodeId);

  if (!runId) return null;

  const mappedNodeRuns: NodeRunSnapshot[] = nodeRuns.map((n) => ({
    id: n.id,
    run_id: n.run_id,
    node_id: n.node_id,
    session_key: n.session_key,
    status: n.status,
    input: n.input,
    output: n.output,
    error: undefined,
    started_at: n.started_at,
    finished_at: n.finished_at,
    agent: n.agent,
    model: n.model,
  }));
  const nodeNames: Record<string, string> = {};
  for (const node of run?.graph?.nodes ?? []) {
    if (node?.id) nodeNames[node.id] = node.name || node.id;
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "6px", marginTop: "8px" }} data-testid="run-view-entry">
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
      {open ? (
        <>
          {error ? <div style={{ fontSize: "12px", color: "var(--danger-color, #ef4444)" }}>{error}</div> : null}
          {run ? (
            <RunTimeline
              run={run}
              nodeRuns={mappedNodeRuns}
              nodeNames={nodeNames}
              onOpenSession={onOpenSession}
              onBackToComments={collapse}
            />
          ) : !error ? (
            <div style={{ fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.runTimeline.loading")}</div>
          ) : null}
        </>
      ) : null}
    </div>
  );
}
