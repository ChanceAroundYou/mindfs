/**
 * 会话列表项模型与映射。
 * toSessionItem 是服务端原始会话 → 前端列表项的唯一转换点。
 * 拆自 appSupport.tsx（2026-09 App.tsx 拆分）。
 */

import { normalizeFastService } from "./appTask";

import {  QueuedUserMessage ,  RelatedFile ,  RelatedWorktree ,  Session ,  TokenUsage  } from "../services/session";

export type SessionMode = "chat" | "plugin" | "command";

export type WSStatus = "connecting" | "connected" | "reconnecting" | "disconnected";

export function isTopLevelSessionItem(session: SessionItem): boolean {
  return !String(session?.parent_session_key || "").trim();
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

export function hasSessionExchanges(session: Session | null | undefined): boolean {
  return Array.isArray(session?.exchanges) && session.exchanges.length > 0;
}

export function normalizeMode(mode: SessionMode | undefined): SessionMode {
  if (mode === "plugin") return mode;
  if (mode === "command") return mode;
  return "chat";
}
