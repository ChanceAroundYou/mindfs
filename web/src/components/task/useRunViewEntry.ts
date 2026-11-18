import { useCallback, useEffect, useRef, useState } from "react";
import { fetchTaskEngine, fetchWorkflowNodeRuns, fetchWorkflowRun } from "../../services/task/runView";
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
  /** 节点会话仍在决定中：直接跳会话的按钮据此禁用，不把「还没取到」当成「没有」。 */
  nodeRunsLoading: boolean;
  error: string;
  gateError: string;
  decidingNodeRunId: string;
  toggle: () => void;
  decideGate: (nodeRun: NodeRunSnapshot, decision: HumanGateDecision) => Promise<void>;
};

export function useRunViewEntry(
  rootId: string,
  taskId: string,
  nodeId?: string,
  refreshKey?: string,
): RunViewState {
  const [runId, setRunId] = useState("");
  const [open, setOpen] = useState(false);
  const [run, setRun] = useState<WorkflowRunPayload | null>(null);
  const [nodeRuns, setNodeRuns] = useState<NodeRunPayload[]>([]);
  const [nodeRunsLoading, setNodeRunsLoading] = useState(false);
  const [error, setError] = useState("");
  const [gateError, setGateError] = useState("");
  const [decidingNodeRunId, setDecidingNodeRunId] = useState("");
  const busyRef = useRef(false);
  const openRef = useRef(false);
  const identityRef = useRef("");
  const loadedRunIdRef = useRef("");

  const loadRun = useCallback(async () => {
    const [runPayload, nodePayload] = await Promise.all([
      fetchWorkflowRun(rootId, taskId, nodeId),
      fetchWorkflowNodeRuns(rootId, taskId, nodeId),
    ]);
    setRun(runPayload);
    setNodeRuns(nodePayload);
    setError("");
  }, [rootId, taskId, nodeId]);

  // 任务身份变化（切换卡片/节点）时整体重置；refreshKey 变化只重新探测引擎。
  // 引擎绑定在对话回合开始时才从 legacy 变成 workflow，只在挂载时探测一次会让
  // 新建任务当场跑完 workflow 也看不到运行视图。
  useEffect(() => {
    const identity = `${rootId}|${taskId}|${nodeId || ""}`;
    if (identityRef.current !== identity) {
      identityRef.current = identity;
      setRunId("");
      setOpen(false);
      openRef.current = false;
      setRun(null);
      setNodeRuns([]);
      setError("");
      setGateError("");
      setDecidingNodeRunId("");
      busyRef.current = false;
      loadedRunIdRef.current = "";
    }
    let cancelled = false;
    fetchTaskEngine(rootId, taskId, nodeId)
      .then((engine) => {
        if (cancelled) return;
        const next = engine?.engine === "workflow" ? engine.workflow_run_id || "" : "";
        setRunId(next);
      })
      .catch(() => {
        // 探测失败只意味着「没迁移过/节点不可达」，不在留言板上打扰用户。
      });
    return () => {
      cancelled = true;
    };
  }, [rootId, taskId, nodeId, refreshKey]);

  // run 换了一条（新回合绑定）时旧数据立即作废；如果入口正展开，直接拉新 run，
  // 否则等下次展开再取，避免折叠状态也发请求。
  useEffect(() => {
    if (loadedRunIdRef.current === runId) return;
    loadedRunIdRef.current = runId;
    setRun(null);
    setNodeRuns([]);
    setError("");
    setGateError("");
    setDecidingNodeRunId("");
  }, [runId, rootId, taskId, nodeId]);

  // 任务详情页的「跳到工作流会话」按钮不需要展开运行视图，所以节点行不能等
  // toggle：引擎一绑定到 workflow 就取一次。按 refreshKey 重取是因为节点会话
  // 是回合中途才绑定的，第一次取到的行可能还没有 session_key。
  useEffect(() => {
    if (!runId) {
      setNodeRunsLoading(false);
      return undefined;
    }
    let cancelled = false;
    setNodeRunsLoading(true);
    fetchWorkflowNodeRuns(rootId, taskId, nodeId)
      .then((payload) => {
        if (!cancelled) setNodeRuns(payload);
      })
      .catch(() => {
        // 预取失败只是少了快捷入口；展开运行视图时还会再拉一次并展示错误。
      })
      .finally(() => {
        if (!cancelled) setNodeRunsLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [runId, rootId, taskId, nodeId, refreshKey]);

  const toggle = useCallback(() => {
    if (openRef.current) {
      openRef.current = false;
      setOpen(false);
      return;
    }
    openRef.current = true;
    setOpen(true);
    if (run || !runId) return;
    void loadRun().catch((err) => setError(err instanceof Error ? err.message : String(err)));
  }, [run, runId, loadRun]);

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

  return {
    runId,
    open,
    run,
    nodeRuns,
    nodeRunsLoading,
    error,
    gateError,
    decidingNodeRunId,
    toggle,
    decideGate,
  };
}