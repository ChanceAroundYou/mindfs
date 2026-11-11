import type { StageTemplate, TaskDetail, TaskOverviewItem } from "./types";
import { appURL } from "../net/base";
import { getRootNodeId } from "../net/rootNode";
import { protectedJSON } from "../net/api";

export async function fetchTaskDetails(rootId: string, filters?: {
  status?: string;
  stage?: number;
  after?: string;
  before?: string;
  limit?: number;
  /** 项目内的任务号（每项目自增）。命中即一条 —— 工作台就地开详情就走这条 */
  taskNumber?: number;
}, nodeId?: string): Promise<TaskDetail[]> {
  const params = new URLSearchParams({ root: rootId });
  if (filters?.status) params.set("status", filters.status);
  if (typeof filters?.stage === "number") params.set("stage", String(filters.stage));
  if (filters?.after) params.set("after", filters.after);
  if (filters?.before) params.set("before", filters.before);
  if (typeof filters?.limit === "number" && filters.limit > 0) params.set("limit", String(filters.limit));
  if (typeof filters?.taskNumber === "number" && filters.taskNumber > 0) params.set("task_number", String(filters.taskNumber));
  nodeId = nodeId || getRootNodeId(rootId);
  const payload = await protectedJSON<any>(appURL("/api/tasks", params, nodeId));
  return Array.isArray(payload?.items) ? payload.items : [];
}

export async function createTask(
  rootId: string,
  input: string,
  stages: StageTemplate[],
  createWorktree = false,
  worktreeBranchMode: "new" | "existing" = "new",
  worktreeBranch = "",
  nodeId?: string,
  options?: { name?: string },
): Promise<TaskDetail> {
  nodeId = nodeId || getRootNodeId(rootId);
  return protectedJSON<TaskDetail>(appURL("/api/tasks", undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      root_id: rootId,
      input,
      create_worktree: createWorktree,
      worktree_branch_mode: worktreeBranchMode,
      worktree_branch: worktreeBranch,
      ...(options?.name ? { name: options.name } : {}),
      // 流水是任务的初始快照（AGENTS.md 事实 13），后端不再维护可回查的流水库。
      stages,
    }),
  });
}

export async function updateTaskInput(
  rootId: string,
  taskId: string,
  input: string,
  createWorktree?: boolean,
  worktreeBranchMode?: "new" | "existing",
  worktreeBranch?: string,
  nodeId?: string,
): Promise<TaskDetail> {
  nodeId = nodeId || getRootNodeId(rootId);
  return protectedJSON<TaskDetail>(appURL(`/api/tasks/${encodeURIComponent(taskId)}/input`, undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      root_id: rootId,
      input,
      ...(typeof createWorktree === "boolean" ? { create_worktree: createWorktree } : {}),
      ...(worktreeBranchMode ? { worktree_branch_mode: worktreeBranchMode } : {}),
      ...(typeof worktreeBranch === "string" ? { worktree_branch: worktreeBranch } : {}),
    }),
  });
}

export async function moveTask(rootId: string, taskId: string, action: "next" | "run-now" | "pause" | "resume" | "complete" | "cancel" | "fail", reason = "", nodeId?: string): Promise<TaskDetail> {
  return protectedJSON<TaskDetail>(appURL(`/api/tasks/${encodeURIComponent(taskId)}/${action}`, undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root_id: rootId, reason }),
  });
}

/**
 * 删掉一张任务卡 —— 只有卡片，worktree / 分支 / 会话都不动。
 *
 * 与 cancel（POST .../cancel）是两回事：那个只改状态，卡片照旧留在板上；
 * 这个把卡片拿走，专给终态任务清场用，否则看板只增不减。
 *
 * root_id 走 query：DELETE 带 body 不是所有中间层都转发。
 */
export async function deleteTask(rootId: string, taskId: string, nodeId?: string): Promise<void> {
  const params = new URLSearchParams({ root_id: rootId });
  await protectedJSON<unknown>(appURL(`/api/tasks/${encodeURIComponent(taskId)}`, params, nodeId), {
    method: "DELETE",
  });
}

/**
 * 重建已删除的任务 worktree。
 *
 * 显式动作，服务端不自动重建：目录没了之后那个分支可能还在（代码还在分支上）也可能已经
 * 跟着删了，自动重建等于替用户做这个决定。失败（如 branch already exists）已经把原因
 * 记到任务的 session_error 上，所以这里照样 resolve，由调用方刷详情看那条错误。
 */
export async function rebuildTaskWorktree(rootId: string, taskId: string, nodeId?: string): Promise<TaskDetail> {
  return protectedJSON<TaskDetail>(appURL(`/api/tasks/${encodeURIComponent(taskId)}/rebuild-worktree`, undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root_id: rootId }),
  });
}

export async function fetchTasksOverview(nodeId?: string): Promise<TaskOverviewItem[]> {
  const payload = await protectedJSON<any>(appURL("/api/tasks/overview", undefined, nodeId));
  return Array.isArray(payload?.items) ? payload.items : [];
}

export async function renameTask(rootId: string, taskId: string, name: string, nodeId?: string): Promise<TaskDetail> {
  nodeId = nodeId || getRootNodeId(rootId);
  return protectedJSON<TaskDetail>(appURL(`/api/tasks/${encodeURIComponent(taskId)}/rename`, undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root_id: rootId, name }),
  });
}

export async function addTaskStage(rootId: string, taskId: string, stage: StageTemplate, nodeId?: string): Promise<TaskDetail> {
  nodeId = nodeId || getRootNodeId(rootId);
  return protectedJSON<TaskDetail>(appURL(`/api/tasks/${encodeURIComponent(taskId)}/add-stage`, undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root_id: rootId, stage }),
  });
}

export async function updateTaskStage(rootId: string, taskId: string, index: number, stage: StageTemplate, nodeId?: string): Promise<TaskDetail> {
  nodeId = nodeId || getRootNodeId(rootId);
  return protectedJSON<TaskDetail>(appURL(`/api/tasks/${encodeURIComponent(taskId)}/update-stage`, undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root_id: rootId, index, stage }),
  });
}

export async function removeTaskStage(rootId: string, taskId: string, index: number, nodeId?: string): Promise<TaskDetail> {
  nodeId = nodeId || getRootNodeId(rootId);
  return protectedJSON<TaskDetail>(appURL(`/api/tasks/${encodeURIComponent(taskId)}/remove-stage`, undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root_id: rootId, index }),
  });
}
