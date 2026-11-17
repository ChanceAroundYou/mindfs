/**
 * 任务/看板助手的纯函数。
 * 拆自 appSupport.tsx（2026-09 App.tsx 拆分）。
 */

// 类型依赖使用 import type，避免在运行时引入 i18n 与 task service 模块。
import type { MessageKey, MessageParams } from "../i18n";
import type { KanbanTask, TaskDetail } from "../services/tasks";
import { scopeKey } from "../shared/scope";

/**
 * 任务内联编辑器取 @文件 / /命令 候选时用的 agent。
 *
 * 建任务本身不再携带 agent/model —— 任务的第一轮由 router 决定走哪条执行路径，
 * 这里只影响候选列表从哪个 agent 拿（不同 harness 的候选集不同）。
 */
export const DEFAULT_TASK_AGENT = "claude";

export function taskProjectScopeKey(rootId: string, nodeId?: string | null): string {
  return scopeKey(nodeId || "", rootId);
}

export function parseTaskProjectScopeKey(value: string): { rootId: string; nodeId: string } {
  const key = String(value || "").trim();
  const separator = key.lastIndexOf("::");
  if (separator < 0) return { rootId: key, nodeId: "" };
  return { rootId: key.slice(separator + 2), nodeId: key.slice(0, separator) };
}

/** Explicit node scope never falls back to a same-named project's metadata on another node. */
export function findTaskProjectRoot(
  rootId: string,
  nodeId: string | null | undefined,
  byKey: Record<string, any>,
  byId: Record<string, any>,
): any | undefined {
  const id = String(rootId || "").trim();
  if (!id) return undefined;
  const node = String(nodeId || "").trim();
  return node ? byKey[taskProjectScopeKey(id, node)] : byId[id];
}

export function isTerminalKanbanTask(task: KanbanTask): boolean {
  return task.status === "success" || task.status === "fail" || task.status === "cancelled";
}

export function parseTaskSessionErrorMessage(error?: string): string {
  const raw = String(error || "").trim();
  if (!raw) return "";
  try {
    const parsed = JSON.parse(raw) as { message?: unknown };
    return typeof parsed.message === "string" && parsed.message.trim() ? parsed.message.trim() : raw;
  } catch {
    return raw;
  }
}

export function parseTaskSessionErrorDetails(error?: string): string[] {
  const raw = String(error || "").trim();
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw) as { data?: unknown };
    if (Array.isArray(parsed.data)) return parsed.data.map((item) => String(item)).filter(Boolean);
    if (parsed.data === undefined || parsed.data === null) return [];
    return [String(parsed.data)];
  } catch {
    return [];
  }
}

export function taskStatusLabel(status: string, t: (key: MessageKey, params?: MessageParams) => string): string {
  const labels: Record<string, MessageKey> = {
    pending: "task.status.pending",
    running: "task.status.running",
    waiting_user: "task.status.waitingUser",
    paused: "task.status.paused",
    success: "task.status.success",
    fail: "task.status.fail",
    cancelled: "task.status.cancelled",
    approved: "task.status.approved",
    rejected: "task.status.rejected",
  };
  return labels[status] ? t(labels[status]) : status || "-";
}

/**
 * 任务状态色。看板卡、工作台卡、任务详情面板共用这一份 —— 三处各写一套的话，
 * 「已完成到底该是绿还是灰」迟早会分叉。
 *
 * 色值走 index.css 的 --status-* token，深浅主题各自给值（meadow/moss 是浅色，继承 :root）。
 * 注意别和 stream/ToolCallCard.tsx 里那份 statusColors 混：那份是工具调用的生命周期
 * （in_progress / complete / error），词表不同，混用会让两边互相污染。
 */
export function taskStatusColor(status: string): string {
  const colors: Record<string, string> = {
    pending: "var(--text-secondary)",
    running: "var(--accent-color)",
    waiting_user: "var(--status-warn)",
    paused: "var(--text-secondary)",
    success: "var(--status-ok)",
    approved: "var(--status-ok)",
    fail: "var(--status-bad)",
    rejected: "var(--status-bad)",
    cancelled: "var(--text-secondary)",
  };
  return colors[status] || "var(--text-secondary)";
}

/**
 * 任务的第一条用户留言（卡片正文、内联编辑器回填都用它）。
 *
 * 阶段模型删除后，这条信息只落在建任务时写入的 `task_created` 事件里
 * （payload 是 `{"input": "..."}`）。事件按创建顺序返回，取第一条即可。
 */
export function firstTaskInputFromDetail(detail: TaskDetail): string {
  const events = detail.events || [];
  const created = events.find((event) => event.type === "task_created");
  if (!created?.payload_json) return "";
  try {
    const payload = JSON.parse(created.payload_json) as { input?: unknown };
    return typeof payload.input === "string" ? payload.input : "";
  } catch {
    return "";
  }
}

/**
 * 卡片上要显示的会话入口。对话式任务只有一条主线会话（workflow 节点会话不进
 * 卡片操作行），所以这里就是 main_session_key。
 */
export function taskSessionKeysFromDetail(detail: TaskDetail): string[] {
  const mainKey = String(detail.task.main_session_key || "").trim();
  return mainKey ? [mainKey] : [];
}

export function normalizeFastService(
  value: unknown,
): "" | "on" | "off" {
  return value === "on" || value === "off" ? value : "";
}

export type TaskInlineAttachment = {
  id: string;
  file: File;
  previewUrl?: string;
  isImage: boolean;
};

export type TaskInlineEditState = {
  text: string;
  /**
   * 面板的目标项目。从工作台发起时是那个面板里选的项目，不一定是当前项目；
   * 缺省表示当前项目（看板入口永远落在当前项目上）。
   */
  targetRootId?: string;
  /** 项目 ID 在多个节点可能重名；面板的查询、存储与创建都必须保留目标节点。 */
  targetNodeId?: string;
  /**
   * 面板上是否显示「项目」下拉。只有工作台入口会置 true（它不知道当前在哪个项目）；
   * 看板入口为 false —— 那里项目已经定了，多一个选不了的项目下拉只是噪声。
   * 换项目时要重算 worktree 偏好和 canToggleWorktree，按 nodeId::rootId 分项目保存。
   */
  allowProjectSwitch?: boolean;
  createWorktreePerRoot?: Record<string, { createWorktree: boolean; worktreeBranchMode: "new" | "existing"; worktreeBranch: string }>;
  /** 新建时的任务名 */
  name?: string;
  /**
   * 建完是否立刻开跑。默认 true：首条留言马上交给 Router（和改造前一致）。
   * 勾掉时任务只落卡、留言以 queued 进对话流，等用户在对话里点「立即执行」。
   */
  startImmediately: boolean;
  createWorktree: boolean;
  worktreeBranchMode: "new" | "existing";
  worktreeBranch: string;
  canToggleWorktree: boolean;
  attachments: TaskInlineAttachment[];
};