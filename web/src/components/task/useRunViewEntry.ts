import { useCallback, useEffect, useRef, useState } from "react";
import { fetchTaskEngine, fetchWorkflowNodeRuns, fetchWorkflowRun } from "../../services/task/runView";
import { postTaskHumanGate, type HumanGateDecision } from "../../services/task/humanGate";
import type { WorkflowRunNodeRuns } from "../../shared/taskComments";
import type { NodeRunSnapshot } from "./RunTimeline";
import { legacyRunsFromNodeRuns } from "./runTimelineModel";

// 运行视图入口的取数逻辑（.ts，允许 import services —— 视图 .tsx 不允许）。
//
// 只有任务被显式迁移到 WorkflowRun 引擎后 runId 才非空；legacy 任务一律为空，
// 入口不渲染。所有请求显式传 nodeId（跨节点铁律：不传会静默打激活节点）。
//
// HumanGate 决定也在这里发出（视图层不 import services）：一次只允许一个决定在途
// （busyRef 挡住同一帧的重复点击），成功后重拉整份运行历史，让连线/project 状态
// 与后端权威结果一致；错误只展示，不重试、不改写决定。
export type RunViewState = {
  runId: string;
  open: boolean;
  /** 按轮次分组的运行历史（旧的在前）；未取到为 null，空数组是「取到了但一条都没有」。 */
  runs: WorkflowRunNodeRuns[] | null;
  loading: boolean;
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
  const [runs, setRuns] = useState<WorkflowRunNodeRuns[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [gateError, setGateError] = useState("");
  const [decidingNodeRunId, setDecidingNodeRunId] = useState("");
  const busyRef = useRef(false);
  const openRef = useRef(false);
  const identityRef = useRef("");
  const loadedRunIdRef = useRef("");

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
      setRuns(null);
      setLoading(false);
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

  // 引擎换绑到新 Run 时旧轮次立即作废：新回合建的 Run 与旧 Run 的节点行是两套
  // 数据，混在一份 runs 里会让「第 N 轮」错位。
  useEffect(() => {
    if (loadedRunIdRef.current === runId) return;
    loadedRunIdRef.current = runId;
    setRuns(null);
    setError("");
    setGateError("");
    setDecidingNodeRunId("");
  }, [runId]);

  // 展开时才取数（折叠状态不发请求）。refreshKey 也重取：新回合开始的 Run 与
  // 节点行都是回合中途才出现的，不跟着重取就永远停在展开那一刻的快照。
  //
  // 旧后端（还没升级、只回平铺 node_runs、没有 runs）在这里就地归一化成「一轮」：
  // 不归一化的话展示层拿到 runs=null 会「加载结束、什么都不画」，升级窗口里运行
  // 视图就是一片空白。归一化只在这一层，视图与纯函数仍然只认一种形状。
  useEffect(() => {
    if (!open || !runId) {
      setLoading(false);
      return undefined;
    }
    let cancelled = false;
    setLoading(true);
    fetchWorkflowNodeRuns(rootId, taskId, nodeId)
      .then(async (result) => {
        if (cancelled) return;
        if (result.runs !== null) {
          setRuns(result.runs);
        } else {
          // 旧后端：补取引擎绑定的 Run 快照，把平铺节点行归一化成「一轮」。
          const snapshot = await fetchWorkflowRun(rootId, taskId, nodeId).catch(() => null);
          if (cancelled) return;
          setRuns(legacyRunsFromNodeRuns(result.nodeRuns, snapshot, runId, taskId));
        }
        setError("");
      })
      .catch((err) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, runId, rootId, taskId, nodeId, refreshKey]);

  const toggle = useCallback(() => {
    openRef.current = !openRef.current;
    setOpen(openRef.current);
  }, []);

  const decideGate = useCallback(
    async (nodeRun: NodeRunSnapshot, decision: HumanGateDecision) => {
      if (busyRef.current) return;
      busyRef.current = true;
      setDecidingNodeRunId(nodeRun.id);
      setGateError("");
      try {
        await postTaskHumanGate(rootId, taskId, { workflowNodeId: nodeRun.node_id, decision }, nodeId);
        const result = await fetchWorkflowNodeRuns(rootId, taskId, nodeId);
        if (result.runs !== null) {
          setRuns(result.runs);
        } else {
          const snapshot = await fetchWorkflowRun(rootId, taskId, nodeId).catch(() => null);
          setRuns(legacyRunsFromNodeRuns(result.nodeRuns, snapshot, runId, taskId));
        }
      } catch (err) {
        setGateError(err instanceof Error ? err.message : String(err));
      } finally {
        busyRef.current = false;
        setDecidingNodeRunId("");
      }
    },
    [rootId, taskId, nodeId, runId],
  );

  return {
    runId,
    open,
    runs,
    loading,
    error,
    gateError,
    decidingNodeRunId,
    toggle,
    decideGate,
  };
}
