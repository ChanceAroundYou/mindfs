import type { TaskDetail, TaskOverviewItem } from "./types";
import { appURL } from "../net/base";
import { getRootNodeId } from "../net/rootNode";
import { protectedJSON } from "../net/api";

export async function fetchTaskDetails(rootId: string, filters?: {
  status?: string;
  after?: string;
  /** 归档状态水位（archived_at > 它）。归档不推动 updated_at，必须单独带一条。 */
  archivedAfter?: string;
  before?: string;
  limit?: number;
  /** 项目内的任务号（每项目自增）。命中即一条 —— 工作台就地开详情就走这条 */
  taskNumber?: number;
}, nodeId?: string): Promise<TaskDetail[]> {
  const params = new URLSearchParams({ root: rootId });
  if (filters?.status) params.set("status", filters.status);
  if (filters?.after) params.set("after", filters.after);
  if (filters?.archivedAfter) params.set("archived_after", filters.archivedAfter);
  if (filters?.before) params.set("before", filters.before);
  if (typeof filters?.limit === "number" && filters.limit > 0) params.set("limit", String(filters.limit));
  if (typeof filters?.taskNumber === "number" && filters.taskNumber > 0) params.set("task_number", String(filters.taskNumber));
  nodeId = nodeId || getRootNodeId(rootId);
  const payload = await protectedJSON<any>(appURL("/api/tasks", params, nodeId));
  return Array.isArray(payload?.items) ? payload.items : [];
}

/**
 * 建任务 = 一条用户留言 + 可选的 worktree 配置。
 *
 * 不再随包带流水/阶段：后端把这条留言交给 router，由它决定这一轮是直接回话、
 * 还是挑一个已发布的 workflow 来跑（AGENTS.md 事实 13）。
 *
 * startImmediately=false 时首条留言以 queued 状态落库、不派发，任务停在
 * 「待开始」，之后由 {@link runTaskNow} 再交给 router。
 */
export async function createTask(
  rootId: string,
  input: string,
  createWorktree = false,
  worktreeBranchMode: "new" | "existing" = "new",
  worktreeBranch = "",
  nodeId?: string,
  options?: { name?: string; startImmediately?: boolean },
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
      start_immediately: options?.startImmediately ?? true,
      ...(options?.name ? { name: options.name } : {}),
    }),
  });
}

/**
 * 立即执行：把排队中的首条留言交给 router。
 * 没有排队留言时服务端是幂等空操作（纯对话任务的回合仍由发留言驱动）。
 */
export async function runTaskNow(rootId: string, taskId: string, nodeId?: string): Promise<TaskDetail> {
  nodeId = nodeId || getRootNodeId(rootId);
  return protectedJSON<TaskDetail>(appURL(`/api/tasks/${encodeURIComponent(taskId)}/run-now`, undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root_id: rootId }),
  });
}

export async function moveTask(rootId: string, taskId: string, action: "pause" | "resume" | "complete" | "cancel" | "fail" | "reopen", reason = "", nodeId?: string): Promise<TaskDetail> {
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
 * 归档 / 取消归档一张终态任务卡。
 *
 * 归档只打标记：卡片从工作台隐藏、留在看板「已结束 → 归档」子组；服务端还会给
 * 相关会话打归档标记，但**不删**任何后代会话或转录。取消归档只清任务自己的标记，
 * 不会反过来解开会话（会话可能是用户自己归档的）。
 */
export async function archiveTask(rootId: string, taskId: string, archived: boolean, nodeId?: string): Promise<TaskDetail> {
  nodeId = nodeId || getRootNodeId(rootId);
  return protectedJSON<TaskDetail>(appURL(`/api/tasks/${encodeURIComponent(taskId)}/archive`, undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root_id: rootId, archived }),
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
