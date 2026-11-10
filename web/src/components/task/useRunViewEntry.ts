import { useCallback, useEffect, useState } from "react";
import { fetchTaskEngine, fetchWorkflowNodeRuns, fetchWorkflowRun } from "../../services/task/comments";
import type { NodeRunPayload, WorkflowRunPayload } from "../../shared/taskComments";

// 运行视图入口的取数逻辑（.ts，允许 import services —— 视图 .tsx 不允许）。
//
// 只有任务被显式迁移到 WorkflowRun 引擎后 runId 才非空；legacy 任务一律为空，
// 入口不渲染。所有请求显式传 nodeId（跨节点铁律：不传会静默打激活节点）。
export type RunViewState = {
  runId: string;
  open: boolean;
  run: WorkflowRunPayload | null;
  nodeRuns: NodeRunPayload[];
  error: string;
  toggle: () => void;
  collapse: () => void;
};

export function useRunViewEntry(rootId: string, taskId: string, nodeId?: string): RunViewState {
  const [runId, setRunId] = useState("");
  const [open, setOpen] = useState(false);
  const [run, setRun] = useState<WorkflowRunPayload | null>(null);
  const [nodeRuns, setNodeRuns] = useState<NodeRunPayload[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    setRunId("");
    setOpen(false);
    setRun(null);
    setNodeRuns([]);
    setError("");
    fetchTaskEngine(rootId, taskId, nodeId)
      .then((engine) => {
        if (cancelled) return;
        if (engine?.engine === "workflow" && engine.workflow_run_id) setRunId(engine.workflow_run_id);
      })
      .catch(() => {
        // 探测失败只意味着「没迁移过/节点不可达」，不在留言板上打扰用户。
      });
    return () => {
      cancelled = true;
    };
  }, [rootId, taskId, nodeId]);

  const collapse = useCallback(() => setOpen(false), []);

  const toggle = useCallback(() => {
    if (open) {
      setOpen(false);
      return;
    }
    setOpen(true);
    if (run || !runId) return;
    void Promise.all([
      fetchWorkflowRun(rootId, taskId, nodeId),
      fetchWorkflowNodeRuns(rootId, taskId, nodeId),
    ])
      .then(([runPayload, nodePayload]) => {
        setRun(runPayload);
        setNodeRuns(nodePayload);
        setError("");
      })
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
  }, [open, run, runId, rootId, taskId, nodeId]);

  return { runId, open, run, nodeRuns, error, toggle, collapse };
}
