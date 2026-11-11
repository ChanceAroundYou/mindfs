import { useCallback, useEffect, useRef, useState } from "react";
import { fetchTaskEngine, fetchWorkflowNodeRuns, fetchWorkflowRun } from "../../services/task/comments";
import { postTaskHumanGate, type HumanGateDecision } from "../../services/task/humanGate";
import type { NodeRunPayload, WorkflowRunPayload } from "../../shared/taskComments";
import type { NodeRunSnapshot } from "./RunTimeline";

// 运行视图入口的取数逻辑（.ts，允许 import services —— 视图 .tsx 不允许）。
//
// 只有任务被显式迁移到 WorkflowRun 引擎后 runId 才非空；legacy 任务一律为空，
// 入口不渲染。所有请求显式传 nodeId（跨节点铁律：不传会静默打激活节点）。
//
// HumanGate 决定也在这里发出（视图层不 import services）：一次只允许一个决定在途
// （busyRef 挡住同一帧的重复点击），成功后重拉 run + node runs，让连线/project 状态
// 与后端权威结果一致；错误只展示，不重试、不改写决定。
export type RunViewState = {
  runId: string;
  open: boolean;
  run: WorkflowRunPayload | null;
  nodeRuns: NodeRunPayload[];
  error: string;
  gateError: string;
  decidingNodeRunId: string;
  toggle: () => void;
  collapse: () => void;
  decideGate: (nodeRun: NodeRunSnapshot, decision: HumanGateDecision) => Promise<void>;
};

export function useRunViewEntry(rootId: string, taskId: string, nodeId?: string): RunViewState {
  const [runId, setRunId] = useState("");
  const [open, setOpen] = useState(false);
  const [run, setRun] = useState<WorkflowRunPayload | null>(null);
  const [nodeRuns, setNodeRuns] = useState<NodeRunPayload[]>([]);
  const [error, setError] = useState("");
  const [gateError, setGateError] = useState("");
  const [decidingNodeRunId, setDecidingNodeRunId] = useState("");
  const busyRef = useRef(false);

  useEffect(() => {
    let cancelled = false;
    setRunId("");
    setOpen(false);
    setRun(null);
    setNodeRuns([]);
    setError("");
    setGateError("");
    setDecidingNodeRunId("");
    busyRef.current = false;
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

  const decideGate = useCallback(
    async (nodeRun: NodeRunSnapshot, decision: HumanGateDecision) => {
      if (busyRef.current) return;
      busyRef.current = true;
      setDecidingNodeRunId(nodeRun.id);
      setGateError("");
      try {
        await postTaskHumanGate(rootId, taskId, { workflowNodeId: nodeRun.node_id, decision }, nodeId);
        const [runPayload, nodePayload] = await Promise.all([
          fetchWorkflowRun(rootId, taskId, nodeId),
          fetchWorkflowNodeRuns(rootId, taskId, nodeId),
        ]);
        setRun(runPayload);
        setNodeRuns(nodePayload);
      } catch (err) {
        setGateError(err instanceof Error ? err.message : String(err));
      } finally {
        busyRef.current = false;
        setDecidingNodeRunId("");
      }
    },
    [rootId, taskId, nodeId],
  );

  return { runId, open, run, nodeRuns, error, gateError, decidingNodeRunId, toggle, collapse, decideGate };
}
