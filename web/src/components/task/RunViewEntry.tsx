import React, { useCallback, useEffect, useState } from "react";
import { useI18n } from "../../i18n/index";
import { fetchTaskEngine, fetchWorkflowRun } from "../../services/task/comments";
import { RunTimeline, type NodeRunSnapshot } from "./RunTimeline";

// 运行视图入口：挂在留言板**之下**的一条可展开行。
//
// 为什么做成独立组件：TaskDetailPanel 只加一行接线（避免它继续膨胀），
// 取数与折叠状态都留在这里。层级仍是「留言板的下钻」——入口出现在留言板之后、
// 展开后由 RunTimeline 自己带「返回留言板」按钮（data-drill-in="comment-run"）。
//
// 何时出现：只有任务被显式迁移到 WorkflowRun 引擎后才有运行视图可看；
// legacy 任务一概不渲染（引擎绑定由后端 /api/tasks/{id}/engine 决定）。
export type RunViewEntryProps = {
  rootId: string;
  taskId: string;
  nodeId?: string;
  onOpenSession: (sessionKey: string) => void;
};

export function RunViewEntry({ rootId, taskId, nodeId, onOpenSession }: RunViewEntryProps) {
  const { t } = useI18n();
  const [runId, setRunId] = useState<string>("");
  const [open, setOpen] = useState(false);
  const [run, setRun] = useState<Awaited<ReturnType<typeof fetchWorkflowRun>> | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    setRunId("");
    setOpen(false);
    setRun(null);
    setError("");
    fetchTaskEngine(rootId, taskId, nodeId)
      .then((engine) => {
        if (cancelled) return;
        if (engine?.engine === "workflow" && engine.workflow_run_id) setRunId(engine.workflow_run_id);
      })
      .catch(() => {
        // 引擎探测失败只意味着「没有迁移过/节点不可达」，不该在留言板上报错打扰用户。
      });
    return () => {
      cancelled = true;
    };
  }, [rootId, taskId, nodeId]);

  const toggle = useCallback(async () => {
    if (open) {
      setOpen(false);
      return;
    }
    setOpen(true);
    if (run || !runId) return;
    try {
      const payload = await fetchWorkflowRun(runId, nodeId);
      setRun(payload);
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, [open, run, runId, nodeId]);

  if (!runId) return null;

  const nodeRuns: NodeRunSnapshot[] = [];
  const nodeNames: Record<string, string> = {};
  for (const node of run?.graph?.nodes ?? []) {
    if (node?.id) nodeNames[node.id] = node.name || node.id;
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "6px", marginTop: "8px" }} data-testid="run-view-entry">
      <button
        type="button"
        onClick={() => void toggle()}
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
              nodeRuns={nodeRuns}
              nodeNames={nodeNames}
              onOpenSession={onOpenSession}
              onBackToComments={() => setOpen(false)}
            />
          ) : !error ? (
            <div style={{ fontSize: "12px", color: "var(--text-secondary)" }}>{t("task.runTimeline.loading")}</div>
          ) : null}
        </>
      ) : null}
    </div>
  );
}