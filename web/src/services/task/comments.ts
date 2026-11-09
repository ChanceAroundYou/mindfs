import { appURL } from "../net/base";
import { protectedJSON } from "../net/api";

export type WorkflowChoice = "specified" | "disabled" | "empty";

export type TaskActivity = {
  id: string;
  task_id: string;
  type: string;
  author: string;
  role: "user" | "agent" | string;
  body: string;
  workflow_choice: WorkflowChoice | string;
  workflow_version_id?: string;
  created_at: string;
};

export type ContinueRequest = {
  request_id: string;
  task_id: string;
  comment_id: string;
  status: "queued" | "blocked" | "consumed" | string;
  created_at: string;
  updated_at: string;
};

export type PostCommentResult = {
  activity: TaskActivity;
  continue_request: ContinueRequest;
};

export function newCommentId(): string {
  const c = globalThis.crypto as Crypto | undefined;
  if (c && typeof c.randomUUID === "function") return c.randomUUID();
  return `c-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

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
