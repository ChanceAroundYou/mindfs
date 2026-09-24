// 直接导入标准组件



// 类型定义

import React, { useEffect, useState } from "react";
import { currentUser } from "../services/authGate";
import { getRootNodeId } from "../services/rootNode";
import { scopeKey, dirSelKey } from "../services/scope";
import { type FileEntry, type DirectorySortMode } from "../services/directorySort";
import { type Session, type QueuedUserMessage, type TokenUsage, type RelatedFile, type RelatedWorktree } from "../services/session";
import { type KanbanTask, type StageRun, type StageTemplate, type TaskDetail, type TaskTemplate } from "../services/tasks";
import { type UpdateState } from "../services/update";
import { type MessageKey, type MessageParams } from "../i18n";
import { type PluginInput } from "../plugins/manager";
import { type FilePayload } from "../services/file";

// ---------- 拆自 App.tsx 的顶层工具/类型/存储/图标 ----------

export type SessionMode = "chat" | "plugin" | "command";
export type WSStatus = "connecting" | "connected" | "reconnecting" | "disconnected";

export const CHILD_SESSION_PAGE_SIZE = 100;
export const MULTI_PROJECT_SESSION_LIMIT = 6;
export const SESSION_PAGE_SIZE = 50;
export const APP_DOCUMENT_TITLE = "MindFS";

export function isTopLevelSessionItem(session: SessionItem): boolean {
  return !String(session?.parent_session_key || "").trim();
}

export function firstUserInputTemplate(template: TaskTemplate | null): string {
  const first = template?.stages?.[0]?.snapshot;
  return first?.role === "user" ? first.prompt_template || "" : "";
}

export function firstAgentStage(template: TaskTemplate | null): StageTemplate | null {
  return template?.stages?.map((stage) => stage.snapshot).find((stage) => stage.role === "agent") || null;
}

export function isUnfinishedKanbanTask(task: KanbanTask): boolean {
  return task.status !== "success" && task.status !== "fail" && task.status !== "cancelled";
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
    queued: "task.status.queued",
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

export function firstTaskInputFromDetail(detail: TaskDetail): string {
  return detail.stage_runs.find((run) => run.stage_index === 0)?.input || "";
}

export function latestTaskStageRun(detail: TaskDetail, stageIndex: number): StageRun | null {
  const runs = detail.stage_runs
    .filter((run) => run.stage_index === stageIndex)
    .sort((a, b) => {
      const aTime = Date.parse(a.created_at || a.updated_at || "");
      const bTime = Date.parse(b.created_at || b.updated_at || "");
      return (Number.isFinite(bTime) ? bTime : 0) - (Number.isFinite(aTime) ? aTime : 0);
    });
  return runs[0] || null;
}

export function currentTaskInputFromDetail(detail: TaskDetail): string {
  return latestTaskStageRun(detail, detail.task.current_stage_index)?.input || "";
}

export function previousTaskInputsFromDetail(detail: TaskDetail, t: (key: MessageKey, params?: MessageParams) => string): Array<{ id: string; label: string; input: string }> {
  const items: Array<{ id: string; label: string; input: string }> = [];
  for (let index = 0; index < detail.task.current_stage_index; index += 1) {
    const run = latestTaskStageRun(detail, index);
    const input = run?.input || "";
    if (!run || !input.trim()) continue;
    items.push({
      id: run.id,
      label: run.stage_name || t("task.stageLabel", { index: index + 1 }),
      input,
    });
  }
  return items;
}

export function taskSessionKeysFromDetail(detail: TaskDetail): string[] {
  const seen = new Set<string>();
  const keys: string[] = [];
  for (const run of detail.stage_runs) {
    const key = String(run.session_key || "").trim();
    if (!key || seen.has(key)) continue;
    seen.add(key);
    keys.push(key);
  }
  const mainKey = String(detail.task.main_session_key || "").trim();
  if (mainKey && !seen.has(mainKey)) {
    keys.push(mainKey);
  }
  return keys;
}

export function normalizeFastService(
  value: unknown,
): "" | "on" | "off" {
  return value === "on" || value === "off" ? value : "";
}

export type SessionItem = {
  key: string;
  session_key: string;
  root_id?: string;
  _nodeId?: string;
  name?: string;
  type?: SessionMode;
  parent_session_key?: string;
  parent_tool_call_id?: string;
  agent?: string;
  model?: string;
  shell?: string;
  source?: string;
  task_id?: string;
  mode?: string;
  effort?: string;
  fast_service?: "" | "on" | "off";
  plan_mode?: boolean;
  scope?: string;
  purpose?: string;
  created_at?: string;
  updated_at?: string;
  pinned_at?: string | null;
  closed_at?: string;
  title?: string;
  agent_session_id?: string;
  context_window?: {
    totalTokens: number;
    modelContextWindow: number;
  };
  search_seq?: number;
  search_target_id?: string;
  search_snippet?: string;
  search_match_type?: "name" | "user" | "reply";
  related_files?: RelatedFile[];
  related_worktree?: RelatedWorktree | null;
  exchanges?: Array<{
    seq?: number;
    role?: string;
    agent?: string;
    content?: string;
    thought_id?: string;
    timestamp?: string;
    model?: string;
    model_display_name?: string;
	    mode?: string;
	    effort?: string;
	    fast_service?: "" | "on" | "off";
	    context_window?: {
      totalTokens: number;
      modelContextWindow: number;
    };
    token_usage?: TokenUsage;
  }>;
  pending?: boolean;
};

export type MultiProjectSessionGroup = {
  rootId: string;
  rootName: string;
  latestSessionTime: string;
  sessions: SessionItem[];
  totalCount: number;
  _nodeId?: string;
  _nodeColor?: string;
  _nodeName?: string;
};

export type SlashCommandResult = {
  rootId: string;
  sessionKey: string;
  requestId: string;
  command: string;
  content: string;
  status: "running" | "complete" | "failed";
  error?: string;
  createdAt?: number;
  loginNotice?: {
    status?: string;
    loginId?: string;
    verificationUrl?: string;
    userCode?: string;
    error?: string;
    authMode?: string;
    planType?: string;
  };
};

export type TaskInlineAttachment = {
  id: string;
  file: File;
  previewUrl?: string;
  isImage: boolean;
};

export type TaskInlineEditState = {
  taskId?: string;
  templateId: string;
  templateName: string;
  text: string;
  previousInputs: Array<{ id: string; label: string; input: string }>;
  createWorktree: boolean;
  worktreeBranchMode: "new" | "existing";
  worktreeBranch: string;
  canToggleWorktree: boolean;
  attachments: TaskInlineAttachment[];
};

export function latestExchangeText(
  exchanges: unknown,
  field: "agent" | "mode" | "effort" | "fast_service",
): string {
  if (!Array.isArray(exchanges)) {
    return "";
  }
  for (let i = exchanges.length - 1; i >= 0; i -= 1) {
    const value = (exchanges[i] as Record<string, unknown> | null)?.[field];
    if (typeof value === "string" && value.trim()) {
      return value;
    }
  }
  return "";
}

export function sessionInputHistory(session: { exchanges?: Array<{ role?: string; content?: string }> } | null | undefined): string[] {
  const exchanges = Array.isArray(session?.exchanges) ? session.exchanges : [];
  const items: string[] = [];
  for (const exchange of exchanges) {
    if (exchange?.role !== "user") {
      continue;
    }
    const content = String(exchange.content || "").trim();
    if (content) {
      items.push(content);
    }
  }
  return items;
}

export function toSessionItem(
  rootID: string | null | undefined,
  session: any,
): SessionItem | null {
  if (!session) {
    return null;
  }
  const key = session?.key || session?.session_key || "";
  const nextRoot =
    (session?.root_id as string | undefined) || String(rootID || "");
  if (!key || !nextRoot) {
    return null;
  }
  return {
    key,
    session_key: key,
    root_id: nextRoot,
    name: typeof session?.name === "string" ? session.name : "",
    type: normalizeMode(session?.type),
    parent_session_key:
      typeof session?.parent_session_key === "string"
        ? session.parent_session_key
        : undefined,
    parent_tool_call_id:
      typeof session?.parent_tool_call_id === "string"
        ? session.parent_tool_call_id
        : undefined,
    source: typeof session?.source === "string" ? session.source : undefined,
    task_id: typeof session?.task_id === "string" ? session.task_id : undefined,
    agent:
      typeof session?.agent === "string" && session.agent.trim()
        ? session.agent
        : latestExchangeText(session?.exchanges, "agent"),
    model: typeof session?.model === "string" ? session.model : "",
    shell: typeof session?.shell === "string" ? session.shell : "",
    mode:
      typeof session?.mode === "string" && session.mode.trim()
        ? session.mode
        : latestExchangeText(session?.exchanges, "mode"),
    effort:
      typeof session?.effort === "string" && session.effort.trim()
        ? session.effort
        : latestExchangeText(session?.exchanges, "effort"),
    fast_service:
      normalizeFastService(session?.fast_service) ||
      normalizeFastService(latestExchangeText(session?.exchanges, "fast_service")),
    plan_mode:
      typeof session?.plan_mode === "boolean"
        ? session.plan_mode
        : false,
    scope: typeof session?.scope === "string" ? session.scope : "",
    purpose: typeof session?.purpose === "string" ? session.purpose : "",
    created_at:
      typeof session?.created_at === "string" ? session.created_at : undefined,
    updated_at:
      typeof session?.updated_at === "string" ? session.updated_at : undefined,
    pinned_at:
      typeof session?.pinned_at === "string" && session.pinned_at
        ? session.pinned_at
        : undefined,
    closed_at:
      typeof session?.closed_at === "string" ? session.closed_at : undefined,
    context_window:
      session?.context_window &&
      Number(session.context_window.totalTokens) > 0 &&
      Number(session.context_window.modelContextWindow) > 0
        ? {
            totalTokens: Number(session.context_window.totalTokens),
            modelContextWindow: Number(session.context_window.modelContextWindow),
          }
        : undefined,
    search_seq:
      typeof session?.search_seq === "number" ? session.search_seq : undefined,
    search_target_id:
      typeof session?.search_target_id === "string"
        ? session.search_target_id
        : undefined,
    search_snippet:
      typeof session?.search_snippet === "string"
        ? session.search_snippet
        : undefined,
    search_match_type:
      session?.search_match_type === "name" ||
      session?.search_match_type === "user" ||
      session?.search_match_type === "reply"
        ? session.search_match_type
        : undefined,
    related_files: Array.isArray(session?.related_files)
      ? session.related_files
      : undefined,
    related_worktree:
      session?.related_worktree === null
        ? null
        : session?.related_worktree && typeof session.related_worktree === "object"
          ? session.related_worktree
          : undefined,
    pending: typeof session?.pending === "boolean" ? session.pending : undefined,
    // 多节点同名项目：记录会话归属节点，选择会话时按节点路由
    ...(typeof (session as any)?._nodeId === "string" && (session as any)._nodeId
      ? { _nodeId: (session as any)._nodeId as string }
      : {}),
  } as SessionItem;
}
export type Exchange = {
  role: string;
  agent?: string;
  model?: string;
  model_display_name?: string;
  mode?: string;
  effort?: string;
  fast_service?: "" | "on" | "off";
  content?: string;
  thought_id?: string;
  context_window?: {
    totalTokens: number;
    modelContextWindow: number;
  };
  token_usage?: TokenUsage;
  timestamp?: string;
  toolCall?: any;
  todoUpdate?: any;
  planUpdate?: any;
  compactNotice?: any;
  pending_ack?: boolean;
};
export type PendingSend = {
  rootId: string;
  mode: SessionMode;
  agent: string;
  model?: string;
  model_display_name?: string;
  agentMode?: string;
  effort?: string;
  fastService?: "" | "on" | "off";
  shell?: string;
  message: string;
  timestamp: string;
  requestId?: string;
  sessionKey?: string;
  tempKey?: string;
};
export type SessionQueueItem = QueuedUserMessage;
export type ViewerSelection = {
  filePath: string;
  text?: string;
  startLine?: number;
  endLine?: number;
};

export type AttachedFileContext = {
  filePath: string;
  fileName: string;
  startLine?: number;
  endLine?: number;
  text?: string;
};
export type GitFileStat = {
  status: string;
  additions: number;
  deletions: number;
};
export type RelatedFileClickTarget = {
  path: string;
  head?: string;
  repo_path?: string;
  repo_name?: string;
  repo_kind?: string;
};

export function relatedFileSelectionKey(file: RelatedFileClickTarget | null | undefined): string {
  if (!file?.path) return "";
  return [
    file.repo_kind || "",
    file.repo_path || "",
    file.head || "",
    file.path,
  ].join("\0");
}
export type URLState = {
  root: string;
  node?: string;
  file: string;
  session: string;
  cursor: number;
  pluginQuery: Record<string, string>;
  // 主面板模式也进 URL：按钮高亮与面板显示必须同源恢复（缺省时回落 localStorage）。
  view?: MainViewMode;
};
export type ManagedRootPayload = {
  id: string;
  display_name?: string;
  root_path?: string;
  is_git_repo?: boolean;
  is_git_worktree?: boolean;
  size?: number;
  mtime?: string;
};
export type LocalDirItemPayload = {
  name?: string;
  path?: string;
  is_dir?: boolean;
  is_added_root?: boolean;
  root_id?: string;
};
export type LocalDirsPayload = {
  path?: string;
  parent?: string;
  volumes?: LocalDirItemPayload[];
  items?: LocalDirItemPayload[];
};

export function managedDirAddErrorMessage(error: unknown, fallback: string, t: (key: MessageKey, params?: MessageParams) => string): string {
  const message = error instanceof Error ? error.message : String(error || "");
  if (message.includes("root name already exists")) {
    return t("root.nameAlreadyExists");
  }
  return message || fallback;
}

export const PLUGIN_QUERY_STORAGE_PREFIX = "vp-progress:";
export const TREE_SORT_STORAGE_KEY = "mindfs-tree-sort-mode";
export const DIRECTORY_SORT_OVERRIDES_STORAGE_KEY = "mindfs-directory-sort-overrides";
export const FILE_SCROLL_STORAGE_KEY = "mindfs-file-scroll-positions";
export const LAST_ROOT_STORAGE_KEY = "mindfs-last-root-id";
export const LAST_ROOT_NODE_STORAGE_KEY = "mindfs-last-root-node";

// 「上次打开的项目」必须按账户存：它是账户作用域的（项目列表按账户分），
// 不分区的话换账户后会恢复**上一个账户**的项目为当前项目，随后拿它去请求
// 会话/看板 → 该账户根本没有这个项目 → 报错（实测踩过：登录后看板报错）。
export function accountScopedKey(base: string): string {
  const name = String(currentUser()?.username || "").trim();
  return name ? `${base}::${name}` : base;
}
export const GIT_STATUS_EXPANDED_STORAGE_KEY = "mindfs-git-status-expanded";
export const GIT_HISTORY_EXPANDED_STORAGE_KEY = "mindfs-git-history-expanded";
export const TASK_TEMPLATE_SELECTION_STORAGE_KEY = "mindfs-task-template-selection";
export const TASK_TEMPLATE_ALL_FILTER = "__all__";
export const CANDIDATE_FETCH_DEBOUNCE_MS = 512;
export const FILE_TOKEN_PATTERN = /\[(?:read file|file):\s*[^\]]+\]/i;

export function normalizeUpdateState(
  input: UpdateState | null | undefined,
): UpdateState {
  return {
    current_version: input?.current_version || "",
    latest_version: input?.latest_version || "",
    has_update: input?.has_update === true,
    status: input?.status || "idle",
    message: input?.message || "",
    release_name: input?.release_name || "",
    release_body: input?.release_body || "",
    release_url: input?.release_url || "",
    published_at: input?.published_at || "",
    last_checked_at: input?.last_checked_at || "",
    auto_update_supported: input?.auto_update_supported === true,
  };
}

export function updateButtonLabel(state: UpdateState, t: (key: MessageKey, params?: MessageParams) => string): string {
  const status = (state.status || "idle").toLowerCase();
  switch (status) {
    case "available":
      if (state.current_version && state.latest_version) {
        return t("update.available", { current: state.current_version, latest: state.latest_version });
      }
      return state.latest_version ? t("update.toVersion", { version: state.latest_version }) : t("update.newVersion");
    case "downloading":
      return t("update.downloading");
    case "installing":
      return t("update.installing");
    case "restarting":
      return t("update.restarting");
    case "failed":
      return t("update.failed");
    default:
      return t("update.latest");
  }
}

export function updateSummaryText(state: UpdateState, t: (key: MessageKey, params?: MessageParams) => string): string {
  const body = String(state.release_body || "").trim();
  if (body) {
    return body;
  }
  const name = String(state.release_name || "").trim();
  if (name) {
    return name;
  }
  if (state.latest_version) {
    return t("update.foundVersion", { version: state.latest_version });
  }
  return "";
}

export function shouldShowUpdateButton(state: UpdateState): boolean {
  const status = (state.status || "idle").toLowerCase();
  if (
    status === "downloading" ||
    status === "installing" ||
    status === "restarting" ||
    status === "failed"
  ) {
    return true;
  }
  return state.auto_update_supported === true && state.has_update === true;
}

export function buildFileScrollKey(
  rootId: string | null | undefined,
  path: string | null | undefined,
): string {
  if (!rootId || !path) {
    return "";
  }
  return `${rootId}::${path}`;
}

export function hasSessionExchanges(session: Session | null | undefined): boolean {
  return Array.isArray(session?.exchanges) && session.exchanges.length > 0;
}

export function loadPersistedFileScrollPositions(): Record<string, number> {
  if (typeof window === "undefined") {
    return {};
  }
  try {
    const raw = window.localStorage.getItem(FILE_SCROLL_STORAGE_KEY);
    if (!raw) {
      return {};
    }
    const parsed = JSON.parse(raw) as Record<string, unknown>;
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      return {};
    }
    const next: Record<string, number> = {};
    Object.entries(parsed).forEach(([key, value]) => {
      const scrollTop = Number(value);
      if (!key || !Number.isFinite(scrollTop) || scrollTop < 0) {
        return;
      }
      next[key] = scrollTop;
    });
    return next;
  } catch {
    return {};
  }
}

export function persistFileScrollPositions(positions: Record<string, number>): void {
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.localStorage.setItem(
      FILE_SCROLL_STORAGE_KEY,
      JSON.stringify(positions),
    );
  } catch {}
}

export function normalizeMode(mode: SessionMode | undefined): SessionMode {
  if (mode === "plugin") return mode;
  if (mode === "command") return mode;
  return "chat";
}

export function parsePluginQuery(search: string): Record<string, string> {
  const params = new URLSearchParams(search);
  const query: Record<string, string> = {};
  params.forEach((value, key) => {
    if (key.startsWith("vp_")) {
      query[key.slice("vp_".length)] = value;
    }
  });
  return query;
}

export function waitForNextPaint(): Promise<void> {
  if (typeof window === "undefined" || typeof window.requestAnimationFrame !== "function") {
    return new Promise((resolve) => setTimeout(resolve, 0));
  }
  return new Promise((resolve) => {
    window.requestAnimationFrame(() => {
      window.requestAnimationFrame(() => resolve());
    });
  });
}

export function parseCursor(value: string | null): number {
  if (!value) return 0;
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed) || parsed < 0) return 0;
  return parsed;
}

export function normalizeCursor(value: unknown): number | null {
  if (value === undefined || value === null || value === "") {
    return null;
  }
  const parsed = Number.parseInt(String(value), 10);
  if (!Number.isFinite(parsed) || parsed < 0) {
    return 0;
  }
  return parsed;
}

export function isDirectorySortMode(
  value: string | null | undefined,
): value is DirectorySortMode {
  return (
    value === "name-asc" ||
    value === "name-desc" ||
    value === "mtime-desc" ||
    value === "mtime-asc" ||
    value === "size-desc" ||
    value === "size-asc"
  );
}

export function readURLState(): URLState {
  const params = new URLSearchParams(window.location.search);
  const view = params.get("view");
  return {
    root: params.get("root") || "",
    node: params.get("node") || "",
    file: params.get("file") || "",
    session: params.get("session") || "",
    cursor: parseCursor(params.get("cursor")),
    pluginQuery: parsePluginQuery(window.location.search),
    view: MAIN_VIEW_MODES.includes(view as MainViewMode)
      ? (view as MainViewMode)
      : undefined,
  };
}

export function buildURLSearch(next: URLState): string {
  const params = new URLSearchParams();
  if (next.root) params.set("root", next.root);
  if (next.node) params.set("node", next.node);
  if (next.file) params.set("file", next.file);
  if (next.session) params.set("session", next.session);
  if (next.cursor > 0) params.set("cursor", String(next.cursor));
  if (next.view) params.set("view", next.view);
  Object.entries(next.pluginQuery).forEach(([key, value]) => {
    if (!key) return;
    params.set(`vp_${key}`, String(value));
  });
  const encoded = params.toString();
  return encoded ? `?${encoded}` : "";
}

export function pluginQueryStorageKey(
  root: string,
  file: string,
  nodeId?: string,
): string {
  // 多节点同名项目按节点隔离；空 nodeId 与历史格式逐字节一致
  return `${PLUGIN_QUERY_STORAGE_PREFIX}${scopeKey(nodeId, root)}:${file}`;
}

export function loadPersistedPluginQuery(
  root: string,
  file: string,
  nodeId?: string,
): Record<string, string> {
  if (!root || !file) return {};
  try {
    const raw =
      window.localStorage.getItem(pluginQueryStorageKey(root, file, nodeId)) ||
      // 旧版本存的是裸键：scoped 键 miss 时回退，保证历史数据仍能读到
      window.localStorage.getItem(pluginQueryStorageKey(root, file));
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed))
      return {};
    const next: Record<string, string> = {};
    Object.entries(parsed as Record<string, unknown>).forEach(
      ([key, value]) => {
        if (!key) return;
        next[key] = String(value);
      },
    );
    return next;
  } catch {
    return {};
  }
}

export function persistPluginQuery(
  root: string,
  file: string,
  query: Record<string, string>,
  nodeId?: string,
): void {
  if (!root || !file) return;
  try {
    window.localStorage.setItem(
      pluginQueryStorageKey(root, file, nodeId),
      JSON.stringify(query || {}),
    );
  } catch {}
}

export function removeLocalStorageByPrefix(prefix: string): void {
  if (typeof window === "undefined" || !prefix) {
    return;
  }
  try {
    for (const key of Array.from(
      { length: window.localStorage.length },
      (_, index) => window.localStorage.key(index),
    ).filter(Boolean) as string[]) {
      if (key.startsWith(prefix)) {
        window.localStorage.removeItem(key);
      }
    }
  } catch {}
}

export function toPluginInput(
  file: FilePayload,
  query: Record<string, string>,
): PluginInput {
  return {
    name: file.name,
    path: file.path,
    content: file.content,
    ext: file.ext || "",
    mime: file.mime || "",
    size: typeof file.size === "number" ? file.size : 0,
    truncated: !!file.truncated,
    next_cursor:
      typeof file.next_cursor === "number" ? file.next_cursor : undefined,
    query,
  };
}

export function inferReadModeFromPlugin(plugin: any): "incremental" | "full" {
  if (!plugin) return "incremental";
  if (plugin?.fileLoadMode === "full") return "full";
  if (plugin?.fileLoadMode === "incremental") return "incremental";
  return "incremental";
}

export function buildMatchInputFromPath(
  path: string,
  query: Record<string, string>,
): PluginInput {
  const normalized = (path || "").replace(/\\/g, "/");
  const name = normalized.split("/").pop() || normalized;
  const dot = name.lastIndexOf(".");
  const ext = dot >= 0 ? name.slice(dot).toLowerCase() : "";
  return {
    name,
    path: normalized,
    content: "",
    ext,
    mime: "",
    size: 0,
    truncated: false,
    query,
  };
}

export function formatPluginViewContext(value: unknown): string {
  if (typeof value === "string") return value.trim();
  if (value == null) return "";
  try {
    return JSON.stringify(value, null, 2).trim();
  } catch {
    return String(value).trim();
  }
}

export function buildMessageWithViewContext(
  message: string,
  viewContext: unknown,
): string {
  const contextText = formatPluginViewContext(viewContext);
  if (!contextText) return message;
  return [contextText, "", message].join("\n");
}

export function normalizePath(value: string): string {
  return String(value || "")
    .replace(/\\/g, "/")
    .replace(/^\/+|\/+$/g, "");
}

export function relativeDisplayPathFromRoot(rootPath: string | undefined, absolutePath: string): string {
  const root = normalizePath(rootPath || "");
  const target = normalizePath(absolutePath);
  if (!target) return "";
  if (!root) return target;
  if (target === root) return ".";
  if (target.startsWith(`${root}/`)) {
    return target.slice(root.length + 1);
  }
  const rootParts = root.split("/").filter(Boolean);
  const targetParts = target.split("/").filter(Boolean);
  let shared = 0;
  while (
    shared < rootParts.length &&
    shared < targetParts.length &&
    rootParts[shared] === targetParts[shared]
  ) {
    shared += 1;
  }
  const upward = Array(Math.max(0, rootParts.length - shared)).fill("..");
  const downward = targetParts.slice(shared);
  return [...upward, ...downward].join("/") || ".";
}

export function joinDisplayPath(base: string, path: string): string {
  const normalizedBase = normalizePath(base);
  const normalizedPath = normalizePath(path);
  if (!normalizedBase) return normalizedPath;
  if (!normalizedPath) return normalizedBase;
  return `${normalizedBase}/${normalizedPath}`;
}

export function parseFileLocation(path: string): {
  path: string;
  targetLine?: number;
  targetColumn?: number;
} {
  const raw = String(path || "");
  const [base, fragment = ""] = raw.split("#", 2);
  if (fragment) {
    const match = /^L(\d+)(?:C(\d+))?$/i.exec(fragment.trim());
    if (match) {
      const targetLine = Number.parseInt(match[1], 10);
      const targetColumn = match[2] ? Number.parseInt(match[2], 10) : undefined;
      return {
        path: base,
        targetLine:
          Number.isFinite(targetLine) && targetLine > 0 ? targetLine : undefined,
        targetColumn:
          targetColumn && Number.isFinite(targetColumn) && targetColumn > 0
            ? targetColumn
            : undefined,
      };
    }
  }

  const colonMatch = /^(.*):(\d+)(?::(\d+))?$/.exec(base.trim());
  if (!colonMatch) {
    return { path: base };
  }
  const targetLine = Number.parseInt(colonMatch[2], 10);
  const targetColumn = colonMatch[3]
    ? Number.parseInt(colonMatch[3], 10)
    : undefined;
  return {
    path: colonMatch[1],
    targetLine:
      Number.isFinite(targetLine) && targetLine > 0 ? targetLine : undefined,
    targetColumn:
      targetColumn && Number.isFinite(targetColumn) && targetColumn > 0
        ? targetColumn
        : undefined,
  };
}

export function parentDirsOfFile(path: string): string[] {
  const normalized = normalizePath(path);
  if (!normalized) return [];
  const parts = normalized.split("/").filter(Boolean);
  if (parts.length <= 1) return [];
  const dirs: string[] = [];
  for (let i = 1; i < parts.length; i++) {
    dirs.push(parts.slice(0, i).join("/"));
  }
  return dirs;
}

export function dirnameOfPath(path: string): string {
  const normalized = normalizePath(path);
  if (!normalized) return ".";
  const parts = normalized.split("/").filter(Boolean);
  if (parts.length <= 1) return ".";
  return parts.slice(0, -1).join("/");
}

export function basenameOfPath(path: string): string {
  const normalized = normalizePath(path);
  if (!normalized) return "";
  const parts = normalized.split("/").filter(Boolean);
  return parts[parts.length - 1] || normalized;
}

export function mapManagedRootsToEntries(dirs: ManagedRootPayload[]): FileEntry[] {
  return dirs.map((dir) => {
    const d = dir as any;
    return {
      name: dir.display_name || dir.id.split("/").filter(Boolean).pop() || dir.id,
      path: dir.id,
      is_dir: true,
      is_root: true,
      size: typeof dir.size === "number" ? dir.size : undefined,
      mtime: typeof dir.mtime === "string" ? dir.mtime : undefined,
      // ponytail: 多节点着色由 FileTree 通过 rootEntries 扩展字段渲染
      _nodeId: (d as any)._nodeId as string | undefined,
      _nodeColor: d._nodeColor as string | undefined,
      _nodeName: d._nodeName as string | undefined,
    } as FileEntry & { _nodeId?: string; _nodeColor?: string; _nodeName?: string };
  });
}

export function comparableManagedRootPath(value: string | undefined): string {
  return String(value || "").trim().replace(/\\/g, "/").replace(/\/+$/, "");
}

export function buildDirectorySelectionKey(
  nodeId: string | null | undefined,
  root: string,
  path: string,
  isRoot: boolean,
): string {
  return dirSelKey(nodeId, root, path, isRoot);
}

export function loadLastRootId(): string {
  if (typeof window === "undefined") {
    return "";
  }
  return window.localStorage.getItem(accountScopedKey(LAST_ROOT_STORAGE_KEY)) || "";
}

export function loadLastRootNodeId(): string {
  if (typeof window === "undefined") {
    return "";
  }
  return window.localStorage.getItem(accountScopedKey(LAST_ROOT_NODE_STORAGE_KEY)) || "";
}

// 多节点同名项目：以 nodeId::rootId 复合键区分同一 root 在不同节点的副本
export function rootNodeKey(nodeId: string | null | undefined, rootId: string): string {
  return scopeKey(nodeId, rootId);
}

export function indexManagedRoots(dirs: ManagedRootPayload[]): {
  byKey: Record<string, ManagedRootPayload>;
  byId: Record<string, ManagedRootPayload>;
} {
  const byKey: Record<string, ManagedRootPayload> = {};
  const byId: Record<string, ManagedRootPayload> = {};
  for (const dir of dirs || []) {
    const id = String(dir?.id || "");
    if (!id) continue;
    const nid = String((dir as any)._nodeId || "").trim();
    byKey[rootNodeKey(nid, id)] = dir;
    // byId 仅作无节点上下文的回退：同名项目保留首个（本地节点优先），当前选择时再覆盖
    if (!(id in byId)) byId[id] = dir;
  }
  return { byKey, byId };
}

export function loadBooleanRecord(key: string): Record<string, boolean> {
  if (typeof window === "undefined") {
    return {};
  }
  try {
    const parsed = JSON.parse(window.localStorage.getItem(key) || "{}") as Record<string, unknown>;
    return Object.fromEntries(
      Object.entries(parsed).filter(([, value]) => typeof value === "boolean"),
    ) as Record<string, boolean>;
  } catch {
    return {};
  }
}

export function loadStringBooleanRecord(key: string): Record<string, Record<string, boolean>> {
  if (typeof window === "undefined") {
    return {};
  }
  try {
    const parsed = JSON.parse(window.localStorage.getItem(key) || "{}") as Record<string, unknown>;
    return Object.fromEntries(
      Object.entries(parsed).map(([root, value]) => [
        root,
        value && typeof value === "object"
          ? Object.fromEntries(
              Object.entries(value as Record<string, unknown>).filter(([, expanded]) => typeof expanded === "boolean"),
            )
          : {},
      ]),
    ) as Record<string, Record<string, boolean>>;
  } catch {
    return {};
  }
}

export function hasExplicitFileContext(message: string): boolean {
  return FILE_TOKEN_PATTERN.test(message);
}

// Hook for responsive detection
export function useResponsive() {
  const [isMobile, setIsMobile] = useState(false);
  const [isTablet, setIsTablet] = useState(false);
  useEffect(() => {
    const checkSize = () => {
      const width = window.innerWidth;
      setIsMobile(width < 768);
      setIsTablet(width >= 768 && width < 1024);
    };
    checkSize();
    window.addEventListener("resize", checkSize);
    return () => window.removeEventListener("resize", checkSize);
  }, []);
  return { isMobile, isTablet };
}

export type AppProps = {
  onGoHome?: () => void;
};

export const MOBILE_ENTER_KEY_SEND_STORAGE_KEY = "mindfs-mobile-enter-key-sends";
export const SIDEBARS_SWAPPED_STORAGE_KEY = "mindfs-sidebars-swapped";
export const GIT_DIFF_SIDE_BY_SIDE_STORAGE_KEY = "mindfs-git-diff-side-by-side";
export const TASK_CREATE_WORKTREE_PREF_STORAGE_KEY = "mindfs-task-create-worktree-pref";
export type TaskCreateWorktreePreference = {
  createWorktree: boolean;
  worktreeBranchMode: "new" | "existing";
  worktreeBranch: string;
};

// 主区内容的唯一真相源：workspace(跨项目工作台) / board(项目看板) / files(文件列表) / chat(对话)。
// 设计见 docs/main-view-switching-design.md —— 除用户显式点击外，任何代码路径都不得改它。
export type MainViewMode = "workspace" | "board" | "files" | "chat";
export const MAIN_VIEW_STORAGE_KEY = "mindfs-main-view";
export const MAIN_VIEW_MODES: MainViewMode[] = ["workspace", "board", "files", "chat"];

export function loadMainView(): MainViewMode {
  if (typeof window === "undefined") return "board";
  try {
    const saved = window.localStorage.getItem(MAIN_VIEW_STORAGE_KEY);
    return MAIN_VIEW_MODES.includes(saved as MainViewMode) ? (saved as MainViewMode) : "board";
  } catch {
    return "board";
  }
}

// 旧版本按项目记忆主区视图；升级后统一读成新键（看板 → board，文件 → files），只迁移一次。
export function loadLegacyMainView(): MainViewMode | null {
  if (typeof window === "undefined") return null;
  if (window.localStorage.getItem(MAIN_VIEW_STORAGE_KEY)) return null;
  try {
    const legacy = window.localStorage.getItem("mindfs-default-main-content-view");
    const migrated: MainViewMode = legacy === "file-browser" ? "files" : "board";
    window.localStorage.setItem(MAIN_VIEW_STORAGE_KEY, migrated);
    return migrated;
  } catch {
    return null;
  }
}

export function loadMobileEnterKeySends(): boolean {
  if (typeof window === "undefined") {
    return false;
  }
  try {
    return window.localStorage.getItem(MOBILE_ENTER_KEY_SEND_STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}

export function loadSidebarsSwapped(): boolean {
  if (typeof window === "undefined") {
    return false;
  }
  try {
    return window.localStorage.getItem(SIDEBARS_SWAPPED_STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}

export function loadGitDiffSideBySide(): boolean {
  if (typeof window === "undefined") {
    return false;
  }
  try {
    return window.localStorage.getItem(GIT_DIFF_SIDE_BY_SIDE_STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}

export function loadTaskCreateWorktreePreference(rootId: string): TaskCreateWorktreePreference {
  if (typeof window === "undefined" || !rootId) {
    return { createWorktree: false, worktreeBranchMode: "new", worktreeBranch: "" };
  }
  try {
    const parsed = JSON.parse(window.localStorage.getItem(TASK_CREATE_WORKTREE_PREF_STORAGE_KEY) || "{}") as Record<string, unknown>;
    const value = parsed[scopeKey(getRootNodeId(rootId) ?? "", rootId)] as Record<string, unknown> | undefined;
    return {
      createWorktree: value?.createWorktree === true,
      worktreeBranchMode: value?.worktreeBranchMode === "existing" ? "existing" : "new",
      worktreeBranch: typeof value?.worktreeBranch === "string" ? value.worktreeBranch : "",
    };
  } catch {
    return { createWorktree: false, worktreeBranchMode: "new", worktreeBranch: "" };
  }
}

export function saveTaskCreateWorktreePreference(rootId: string, pref: TaskCreateWorktreePreference): void {
  if (typeof window === "undefined" || !rootId) return;
  try {
    const parsed = JSON.parse(window.localStorage.getItem(TASK_CREATE_WORKTREE_PREF_STORAGE_KEY) || "{}") as Record<string, unknown>;
    window.localStorage.setItem(TASK_CREATE_WORKTREE_PREF_STORAGE_KEY, JSON.stringify({
      ...parsed,
      [scopeKey(getRootNodeId(rootId) ?? "", rootId)]: pref,
    }));
  } catch {
    // Ignore storage failures; the current dialog state can still be used.
  }
}
