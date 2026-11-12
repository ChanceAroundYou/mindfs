// 对话式任务活动流的数据层。
//
// 后端契约（server/internal/api/http_activities.go）：
//   GET  /api/tasks/{id}/activities?root_id=...
//   POST /api/tasks/{id}/activities   { root_id, body, workflow_choice, workflow_version_id? }
//   GET  /api/tasks/{id}/activities/{activityId}?root_id=...
//
// root_id 是必需参数：活动库按项目（root）分片，缺了后端直接 400。这里提前拦住，
// 让调用方拿到明确错误，而不是把空 root 发出去再解析 400。
import { appURL } from "./net/base";
import { protectedJSON } from "./net/api";
import type { TaskActivity, WorkflowChoice } from "../shared/taskComments";

export type { ActivityStatus, TaskActivity, WorkflowChoice } from "../shared/taskComments";

export type CreateActivityRequest = {
  body: string;
  workflow_choice: WorkflowChoice;
  workflow_version_id?: string;
};

export type CreateActivityResponse = {
  user_activity: TaskActivity;
  agent_activity?: TaskActivity | null;
};

function requiredRoot(rootId: string): string {
  const root = String(rootId || "").trim();
  if (!root) throw new Error("root_id required");
  return root;
}

export async function listActivities(
  taskId: string,
  rootId: string,
  nodeId?: string,
): Promise<TaskActivity[]> {
  const params = new URLSearchParams({ root_id: requiredRoot(rootId) });
  const payload = await protectedJSON<{ activities?: TaskActivity[] }>(
    appURL(`/api/tasks/${encodeURIComponent(taskId)}/activities`, params, nodeId),
  );
  return Array.isArray(payload?.activities) ? payload.activities : [];
}

export async function createActivity(
  taskId: string,
  rootId: string,
  req: CreateActivityRequest,
  nodeId?: string,
): Promise<CreateActivityResponse> {
  const body: Record<string, unknown> = {
    root_id: requiredRoot(rootId),
    body: req.body,
    workflow_choice: req.workflow_choice,
  };
  // 只在指定 workflow 时带版本，避免空串被后端当成「指定了但版本为空」。
  const versionId = String(req.workflow_version_id || "").trim();
  if (req.workflow_choice === "specified" && versionId) {
    body.workflow_version_id = versionId;
  }
  return protectedJSON<CreateActivityResponse>(
    appURL(`/api/tasks/${encodeURIComponent(taskId)}/activities`, undefined, nodeId),
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    },
  );
}

export async function getActivity(
  taskId: string,
  activityId: string,
  rootId: string,
  nodeId?: string,
): Promise<TaskActivity> {
  const params = new URLSearchParams({ root_id: requiredRoot(rootId) });
  return protectedJSON<TaskActivity>(
    appURL(
      `/api/tasks/${encodeURIComponent(taskId)}/activities/${encodeURIComponent(activityId)}`,
      params,
      nodeId,
    ),
  );
}