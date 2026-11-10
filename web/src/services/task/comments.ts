import { appURL } from "../net/base";
import { protectedJSON } from "../net/api";
import type {
  ContinueRequest,
  NodeRunPayload,
  PostCommentResult,
  TaskActivity,
  TaskExecutionEngine,
  WorkflowChoice,
  WorkflowRunPayload,
} from "../../shared/taskComments";
import { newCommentId } from "../../shared/taskComments";

export type { ContinueRequest, NodeRunPayload, PostCommentResult, TaskActivity, TaskExecutionEngine, WorkflowChoice, WorkflowRunPayload };
export { newCommentId };


export async function fetchTaskComments(rootId: string, taskId: string, nodeId?: string): Promise<TaskActivity[]> {
  const params = new URLSearchParams({ root_id: rootId });
  const payload = await protectedJSON<{ activities?: TaskActivity[] }>(
    appURL(`/api/tasks/${encodeURIComponent(taskId)}/comments`, params, nodeId),
  );
  return Array.isArray(payload?.activities) ? payload.activities : [];
}

export async function postTaskComment(
  rootId: string,
  taskId: string,
  input: { body: string; author?: string; workflowChoice?: WorkflowChoice; workflowVersionId?: string; commentId?: string },
  nodeId?: string,
): Promise<PostCommentResult> {
  const workflowChoice: WorkflowChoice = input.workflowChoice || "empty";
  return protectedJSON<PostCommentResult>(
    appURL(`/api/tasks/${encodeURIComponent(taskId)}/comments`, undefined, nodeId),
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        root_id: rootId,
        comment_id: input.commentId || newCommentId(),
        author: input.author || "user",
        role: "user",
        body: input.body,
        type: "user_comment",
        workflow_choice: workflowChoice,
        ...(workflowChoice === "specified" && input.workflowVersionId
          ? { workflow_version_id: input.workflowVersionId }
          : {}),
      }),
    },
  );
}

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
