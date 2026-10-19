import type {
  FetchExternalSessionsOptions,
  MultiRootSessionGroup,
  RelatedFile,
  Session,
  SessionListPayload,
  SessionSearchHit,
  ToolCall,
} from "./sessionTypes";
import type { FetchSessionsOptions } from "./sessionTypes";
import { getRootNodeId } from "../net/rootNode";
import { appURL } from "../net/base";
import { protectedFetch, protectedJSON, withNodeRetry } from "../net/api";

// 会话的 HTTP 读写（从 services/session.ts 的 SessionService 里抽出，2026-10-07）。
//
// 这一簇全是「拼 URL + 发请求 + 解包」，不碰 WebSocket 连接状态，所以做成自由函数
// 比留在类里更好测：调用方（SessionService 的方法）只是转发参数。
// 注意 `getSession` 是其余几个函数的底座，必须留在同一模块。

/**
 * 导入外部会话是**长任务**：转录可达上百 MB（pc-win 实测 codex rollout 105MB / 160MB），
 * 服务端要整份解析再逐条写 SQLite，耗时随转录大小走。不能套 net/api.ts 那个给扇出**读**
 * 用的 10 秒节点存活 deadline —— 客户端会在服务端还在干活时先判失败，弹出会自动消失的
 * `session.import_failed`；用户再点一次就多出一个同名会话。abort 还有第二重后果：连接一断，
 * 服务端的请求 context 就被取消，块流从此写不进去（行照写），会话进了列表却只有行没有正文
 * —— 机制见 G-Q 的「空壳行」。
 * 自带 signal 即走 withRequestDeadline 的逃生口，30 分钟只给「节点真挂了」一个上界。
 * 完整因果见 docs/upstream-customizations.yaml 的 G-J。
 */
const IMPORT_REQUEST_TIMEOUT_MS = 30 * 60 * 1000;

export async function fetchSessions(
  rootId: string,
  options?: FetchSessionsOptions & { nodeId?: string },
): Promise<SessionListPayload> {
  try {
    (options as any).nodeId = (options as any)?.nodeId || getRootNodeId(rootId);
    const params = new URLSearchParams({ root: rootId });
    if (options?.beforeTime) {
      params.set("before_time", options.beforeTime);
    }
    if (options?.afterTime) {
      params.set("after_time", options.afterTime);
    }
    if (typeof options?.limit === "number" && options.limit > 0) {
      params.set("limit", String(options.limit));
    }
    if (options?.topLevel) {
      params.set("top_level", "1");
    }
    if (options?.includeChildren) {
      params.set("include_children", "1");
    }
    if (options?.archivedOnly) {
      params.set("archived", "only");
    }
    const data = await protectedJSON<any>(appURL("/api/sessions", params, (options as any)?.nodeId));
    if (Array.isArray(data)) {
      return { items: data, pinnedItems: [], pinnedKeys: [], totalCount: data.length };
    }
    const items = Array.isArray(data?.items) ? data.items : [];
    const pinnedItems = Array.isArray(data?.pinned_items) ? data.pinned_items : [];
    const pinnedKeys = Array.isArray(data?.pinned_keys)
      ? data.pinned_keys.map((key: unknown) => String(key || "")).filter(Boolean)
      : pinnedItems.map((item: any) => String(item?.key || item?.session_key || "")).filter(Boolean);
    const totalCount = Number(data?.total_count ?? data?.totalCount ?? items.length) || 0;
    return { items, pinnedItems, pinnedKeys, totalCount };
  } catch (err) {
    console.error("[Session] Failed to fetch sessions:", err);
    return { items: [], pinnedItems: [], pinnedKeys: [], totalCount: 0 };
  }
}

export async function fetchMultiRootSessions(limitPerRoot = 6, nodeId?: string): Promise<MultiRootSessionGroup[]> {
  try {
    const params = new URLSearchParams({ multi_root: "1" });
    if (limitPerRoot > 0) {
      params.set("limit_per_root", String(limitPerRoot));
    }
    const data = await withNodeRetry(() => protectedJSON<any>(appURL("/api/sessions", params, (nodeId as any))));
    const groups = Array.isArray(data?.groups) ? data.groups : [];
    return groups.map((group: any) => ({
      rootId: String(group?.root_id || group?.rootId || ""),
      rootName: String(group?.root_name || group?.rootName || ""),
      latestSessionTime: String(group?.latest_session_time || group?.latestSessionTime || ""),
      items: Array.isArray(group?.items) ? group.items : [],
      pinnedItems: Array.isArray(group?.pinned_items) ? group.pinned_items : [],
      pinnedKeys: Array.isArray(group?.pinned_keys)
        ? group.pinned_keys.map((key: unknown) => String(key || "")).filter(Boolean)
        : Array.isArray(group?.pinned_items)
          ? group.pinned_items.map((item: any) => String(item?.key || item?.session_key || "")).filter(Boolean)
          : [],
      totalCount: Number(group?.total_count ?? group?.totalCount ?? 0) || 0,
    })).filter((group: MultiRootSessionGroup) => !!group.rootId);
  } catch (err) {
    if (err instanceof Error && err.message === "api_not_ready") {
      return [];
    }
    console.error("[Session] Failed to fetch multi-root sessions:", err);
    // 抛出而非吞成 []：调用方要能区分"这个节点没项目"和"这个节点没拉到"，
    // 否则该节点会静默缺席，且没有任何东西会重试（实测可长时间不恢复）
    throw err;
  }
}

export async function fetchChildSessions(
  rootId: string,
  parentSessionKey: string,
  options?: { beforeTime?: string; limit?: number; nodeId?: string },
): Promise<Session[]> {
  try {
    (options as any).nodeId = (options as any)?.nodeId || getRootNodeId(rootId);
    const params = new URLSearchParams({
      root: rootId,
      parent_session_key: parentSessionKey,
    });
    if (options?.beforeTime) {
      params.set("before_time", options.beforeTime);
    }
    if (typeof options?.limit === "number" && options.limit > 0) {
      params.set("limit", String(options.limit));
    }
    const data = await protectedJSON<any[]>(appURL("/api/sessions/children", params, (options as any)?.nodeId));
    return Array.isArray(data) ? data : [];
  } catch (err) {
    console.error("[Session] Failed to fetch child sessions:", err);
    return [];
  }
}

export async function searchSessions(
  rootId: string,
  query: string,
  limit?: number,
  options?: { multiRoot?: boolean; nodeId?: string },
): Promise<SessionSearchHit[]> {
  try {
    (options as any).nodeId = (options as any)?.nodeId || getRootNodeId(rootId);
    const trimmed = query.trim();
    if ((!rootId && !options?.multiRoot) || !trimmed) {
      return [];
    }
    const params = new URLSearchParams({ q: trimmed });
    if (options?.multiRoot) {
      params.set("multi_root", "1");
    } else {
      params.set("root", rootId);
    }
    if (typeof limit === "number" && limit > 0) {
      params.set("limit", String(limit));
    }
    const data = await protectedJSON<any>(appURL("/api/sessions/search", params, (options as any)?.nodeId));
    return Array.isArray(data?.items)
      ? (data.items as SessionSearchHit[])
      : [];
  } catch (err) {
    console.error("[Session] Failed to search sessions:", err);
    return [];
  }
}

export async function getSession(
  rootId: string,
  sessionKey: string,
  seq?: number,
  nodeId?: string,
): Promise<Session | null> {
  try {
    nodeId = nodeId || getRootNodeId(rootId);
    const params = new URLSearchParams({ root: rootId });
    if (typeof seq === "number" && seq > 0) {
      params.set("seq", String(seq));
    }
    const data = await protectedJSON<Session>(
      appURL(`/api/sessions/${encodeURIComponent(sessionKey)}`, params, nodeId),
    );
    return data as Session;
  } catch (err) {
    console.error("[Session] Failed to get session:", err);
    return null;
  }
}


export async function syncExternalSession(
  rootId: string,
  sessionKey: string,
  seq?: number,
  nodeId?: string,
): Promise<Session | null> {
  try {
    nodeId = nodeId || getRootNodeId(rootId);
    const params = new URLSearchParams({ root: rootId });
    if (typeof seq === "number" && seq > 0) {
      params.set("seq", String(seq));
    }
    const data = await protectedJSON<Session>(
      appURL(`/api/sessions/${encodeURIComponent(sessionKey)}/sync`, params, nodeId),
      { method: "POST" },
    );
    return data as Session;
  } catch (err) {
    console.error("[Session] Failed to sync session:", err);
    return null;
  }
}

export async function getToolCall(
  rootId: string,
  sessionKey: string,
  callId: string,
  nodeId?: string,
): Promise<ToolCall | null> {
  try {
    if (!rootId || !sessionKey || !callId) return null;
    nodeId = nodeId || getRootNodeId(rootId);
    const params = new URLSearchParams({ root: rootId });
    const data = await protectedJSON<{ toolcall?: ToolCall; toolCall?: ToolCall } | ToolCall>(
      appURL(
        `/api/sessions/${encodeURIComponent(sessionKey)}/toolcalls/${encodeURIComponent(callId)}`,
        params,
        nodeId,
      ),
    );
    const wrapped = data as { toolcall?: ToolCall; toolCall?: ToolCall };
    if (wrapped?.toolcall) return wrapped.toolcall;
    if (wrapped?.toolCall) return wrapped.toolCall;
    const direct = data as ToolCall;
    return direct?.callId ? direct : null;
  } catch (err) {
    console.error("[Session] Failed to get toolcall:", err);
    return null;
  }
}

export async function getSessionRelatedFiles(
  rootId: string,
  sessionKey: string,
  nodeId?: string,
): Promise<RelatedFile[]> {
  try {
    nodeId = nodeId || getRootNodeId(rootId);
    const params = new URLSearchParams({
      root: rootId,
    });
    const data = await protectedJSON<any[]>(
      appURL(
        `/api/sessions/${encodeURIComponent(sessionKey)}/related-files`,
        params,
        nodeId,
      ),
    );
    return Array.isArray(data) ? (data as RelatedFile[]) : [];
  } catch (err) {
    console.error("[Session] Failed to get session related files:", err);
    return [];
  }
}

export async function removeSessionRelatedFile(
  rootId: string,
  sessionKey: string,
  path: string,
  head = "",
  repoPath = "",
  repoKind = "",
  nodeId?: string,
): Promise<boolean> {
  try {
    nodeId = nodeId || getRootNodeId(rootId);
    const params = new URLSearchParams({ root: rootId, path });
    if (head) {
      params.set("head", head);
    }
    if (repoPath) {
      params.set("repo_path", repoPath);
    }
    if (repoKind) {
      params.set("repo_kind", repoKind);
    }
    const res = await protectedFetch(
      appURL(
        `/api/sessions/${encodeURIComponent(sessionKey)}/related-files`,
        params,
        nodeId,
      ),
      { method: "DELETE" },
    );
    if (!res.ok) {
      throw new Error("Failed to remove session related file");
    }
    return true;
  } catch (err) {
    console.error("[Session] Failed to remove session related file:", err);
    return false;
  }
}

export async function deleteSession(rootId: string, sessionKey: string, nodeId?: string): Promise<boolean> {
  try {
    nodeId = nodeId || getRootNodeId(rootId);
    const params = new URLSearchParams({ root: rootId });
    const res = await protectedFetch(
      appURL(`/api/sessions/${encodeURIComponent(sessionKey)}`, params, nodeId),
      { method: "DELETE" },
    );
    if (!res.ok) {
      throw new Error("Failed to delete session");
    }
    return true;
  } catch (err) {
    console.error("[Session] Failed to delete session:", err);
    return false;
  }
}

export async function renameSession(
  rootId: string,
  sessionKey: string,
  name: string,
  nodeId?: string,
): Promise<Session | null> {
  try {
    nodeId = nodeId || getRootNodeId(rootId);
    const params = new URLSearchParams({ root: rootId });
    const data = await protectedJSON<Session>(
      appURL(
        `/api/sessions/${encodeURIComponent(sessionKey)}/rename`,
        params,
        nodeId,
      ),
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ name }),
      },
    );
    return data as Session;
  } catch (err) {
    console.error("[Session] Failed to rename session:", err);
    return null;
  }
}

export async function setSessionArchived(
  rootId: string,
  sessionKey: string,
  archived: boolean,
  nodeId?: string,
): Promise<Session | null> {
  try {
    nodeId = nodeId || getRootNodeId(rootId);
    const params = new URLSearchParams({ root: rootId });
    const data = await protectedJSON<Session>(
      appURL(
        `/api/sessions/${encodeURIComponent(sessionKey)}/archive`,
        params,
        nodeId,
      ),
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ archived }),
      },
    );
    return data as Session;
  } catch (err) {
    console.error("[Session] Failed to update session archive state:", err);
    return null;
  }
}

export async function forkSession(
  rootId: string,
  sessionKey: string,
  seq: number,
  nodeId?: string,
): Promise<{ session_key: string; session?: Session } | null> {
  try {
    if (!rootId || !sessionKey || !seq) {
      return null;
    }
    nodeId = nodeId || getRootNodeId(rootId);
    return await protectedJSON<{ session_key: string; session?: Session }>(
      appURL("/api/sessions/fork", undefined, nodeId),
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          root_id: rootId,
          session_key: sessionKey,
          seq,
        }),
      },
    );
  } catch (err) {
    console.error("[Session] Failed to fork session:", err);
    throw err;
  }
}

export async function fetchExternalSessions(
  rootId: string,
  agent: string,
  options?: FetchExternalSessionsOptions & { nodeId?: string },
): Promise<Session[]> {
  if (!rootId || !agent) {
    return [];
  }
  (options as any).nodeId = (options as any)?.nodeId || getRootNodeId(rootId);
  const params = new URLSearchParams({ root: rootId, agent });
  if (options?.beforeTime) {
    params.set("before_time", options.beforeTime);
  }
  if (options?.afterTime) {
    params.set("after_time", options.afterTime);
  }
  if (options?.filterBound) {
    params.set("filter_bound", "true");
  }
  if (typeof options?.limit === "number" && options.limit > 0) {
    params.set("limit", String(options.limit));
  }
  const data = await protectedJSON<any[]>(appURL("/api/sessions/external", params, (options as any)?.nodeId));
  return Array.isArray(data) ? data : [];
}

export async function importExternalSession(
  rootId: string,
  agent: string,
  agentSessionId: string,
  nodeId?: string,
): Promise<{ session_key: string } | null> {
  try {
    nodeId = nodeId || getRootNodeId(rootId);
    return await protectedJSON<{ session_key: string }>(appURL("/api/sessions/import", undefined, nodeId), {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
      },
      // 见 IMPORT_REQUEST_TIMEOUT_MS：导入是长任务，不能吃 10 秒的节点存活 deadline。
      signal: AbortSignal.timeout(IMPORT_REQUEST_TIMEOUT_MS),
      body: JSON.stringify({
        root_id: rootId,
        agent,
        agent_session_id: agentSessionId,
      }),
    });
  } catch (err) {
    console.error("[Session] Failed to import external session:", err);
    return null;
  }
}

export async function importExternalSessionsBatch(
  rootId: string,
  agent: string,
  agentSessionIds: string[],
  nodeId?: string,
): Promise<{
  items: Array<{
    agent_session_id: string;
    session_key?: string;
    imported_count?: number;
    success: boolean;
    error?: string;
    error_code?: string;
    error_detail?: string;
    error_path?: string;
    error_operation?: string;
  }>;
} | null> {
  try {
    nodeId = nodeId || getRootNodeId(rootId);
    return await protectedJSON(appURL("/api/sessions/import/batch", undefined, nodeId), {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
      },
      // 见 IMPORT_REQUEST_TIMEOUT_MS：串行导入 N 个会话，10 秒必然不够。
      signal: AbortSignal.timeout(IMPORT_REQUEST_TIMEOUT_MS),
      body: JSON.stringify({
        root_id: rootId,
        agent,
        agent_session_ids: agentSessionIds,
      }),
    });
  } catch (err) {
    console.error("[Session] Failed to import external sessions:", err);
    return null;
  }
}
