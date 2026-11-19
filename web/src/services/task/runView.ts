import { appURL } from "../net/base";
import { protectedJSON } from "../net/api";
import type {
  NodeRunPayload,
  TaskActivity,
  TaskExecutionEngine,
  WorkflowChoice,
  WorkflowRunNodeRuns,
  WorkflowRunPayload,
} from "../../shared/taskComments";

export type { NodeRunPayload, TaskActivity, TaskExecutionEngine, WorkflowChoice, WorkflowRunNodeRuns, WorkflowRunPayload };

export async function fetchTaskEngine(rootId: string, taskId: string, nodeId?: string): Promise<TaskExecutionEngine> {
  const params = new URLSearchParams({ root_id: rootId });
  const payload = await protectedJSON<{ engine?: TaskExecutionEngine }>(
    appURL(`/api/tasks/${encodeURIComponent(taskId)}/engine`, params, nodeId),
  );
  return payload?.engine ?? { task_id: taskId, engine: "legacy" };
}

export async function fetchWorkflowRun(rootId: string, taskId: string, nodeId?: string): Promise<WorkflowRunPayload> {
  const params = new URLSearchParams({ root_id: rootId });
  const payload = await protectedJSON<{ run?: WorkflowRunPayload }>(
    appURL(`/api/tasks/${encodeURIComponent(taskId)}/workflow-run`, params, nodeId),
  );
  if (!payload?.run) throw new Error("workflow run not found");
  return payload.run;
}

export type WorkflowNodeRunsResult = {
  /** 平铺的全部节点执行行（旧形状，仍可用于「最近一次有会话的节点」这类挑选）。 */
  nodeRuns: NodeRunPayload[];
  /** 按 Run 分组的历史轮次（旧的在前）；后端未返回时为 null，展示层回落到单个 Run。 */
  runs: WorkflowRunNodeRuns[] | null;
};

export async function fetchWorkflowNodeRuns(rootId: string, taskId: string, nodeId?: string): Promise<WorkflowNodeRunsResult> {
  const params = new URLSearchParams({ root_id: rootId });
  const payload = await protectedJSON<{ node_runs?: NodeRunPayload[]; runs?: WorkflowRunNodeRuns[] }>(
    appURL(`/api/tasks/${encodeURIComponent(taskId)}/workflow-run/nodes`, params, nodeId),
  );
  return {
    nodeRuns: Array.isArray(payload?.node_runs) ? payload.node_runs : [],
    runs: Array.isArray(payload?.runs) ? payload.runs : null,
  };
}
