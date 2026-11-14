import { appURL } from "../net/base";
import { protectedJSON } from "../net/api";
import type {
  NodeRunPayload,
  TaskActivity,
  TaskExecutionEngine,
  WorkflowChoice,
  WorkflowRunPayload,
} from "../../shared/taskComments";

export type { NodeRunPayload, TaskActivity, TaskExecutionEngine, WorkflowChoice, WorkflowRunPayload };

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

export async function fetchWorkflowNodeRuns(rootId: string, taskId: string, nodeId?: string): Promise<NodeRunPayload[]> {
  const params = new URLSearchParams({ root_id: rootId });
  const payload = await protectedJSON<{ node_runs?: NodeRunPayload[] }>(
    appURL(`/api/tasks/${encodeURIComponent(taskId)}/workflow-run/nodes`, params, nodeId),
  );
  return Array.isArray(payload?.node_runs) ? payload.node_runs : [];
}
