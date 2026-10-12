import { estimateCommandTerminalCols } from "./sessionTerminalLayout";
import type { CachedSessionListRecord, CachedSessionRecord } from "./sessionCacheTypes";
import { appURL, wsURL } from "./net/base";
import { getRootNodeId } from "./net/rootNode";
import { scopeSessionKey } from "../shared/scope";
import { protectedFetch, protectedJSON, withNodeRetry } from "./net/api";
import {
  deleteSession,
  fetchChildSessions,
  fetchExternalSessions,
  fetchMultiRootSessions,
  fetchSessions,
  forkSession,
  getSession,
  getSessionRelatedFiles,
  getToolCall,
  importExternalSession,
  importExternalSessionsBatch,
  removeSessionRelatedFile,
  renameSession,
  searchSessions,
  setSessionArchived,
  syncExternalSession,
} from "./sessionApi";
import { compactContext } from "./sessionContext";
import {
  buildAnswerQuestionMessage,
  buildCancelMessage,
  buildPlanModeMessage,
  buildQueueRemoveMessage,
  buildQueueSendNowMessage,
  buildQueueUpdateMessage,
  buildSessionMessage,
  buildSessionReadyMessage,
  buildSlashCommandMessage,
} from "./sessionOutbound";
import { appendSessionDelta, cloneExchangeAux, getSessionMaxSeq, toPersistentExchangeAux, withSessionMeta } from "./sessionExchangeMerge";
import { createRequestId, generateClientId } from "./sessionIdentifiers";
export {
  getCachedSessionList,
  saveCachedSessionList,
} from "./sessionListCache";
export {
  clearWindowedView,
  getSessionIncrement,
  getSessionWindow,
  isWindowedView,
  setWindowedView,
  syncSession,
} from "./sessionWindow";
import { cloneSession, getCachedSession, saveCachedSession } from "./sessionCacheStore";
export {
  clearCachedSessionsForRoot,
  deleteCachedSession,
  deleteCachedSessionLists,
  getCachedSession,
  setCachedSessionRelatedFiles,
  toPersistentSession,
} from "./sessionCacheStore";
export {
  appendExchangeAuxDelta,
  composeLoadedExchanges,
  dropTransientExchanges,
  getSessionMinSeq,
  isPersistedSeq,
  isSamePendingEcho,
  isTransientExchange,
  mergeSessionExchanges,
  settlePendingAcks,
} from "./sessionExchangeMerge";
import { SessionDispatcher } from "./sessionDispatcher";
import { SessionConnectionManager } from "./sessionConnectionManager";
import type {
  FetchExternalSessionsOptions,
  FetchSessionsOptions,
  MultiRootSessionGroup,
  PendingMessage,
  RelatedFile,
  Session,
  SessionEventHandler,
  SessionListPayload,
  SessionSearchHit,
  SessionServiceEvent,
  SessionType,
  SessionWindow,
  SessionWindowMeta,
  SessionWindowOptions,
  StreamEvent,
  ToolCall,
} from "./sessionTypes";
export type {
  CompactNotice,
  ExchangeAux,
  FetchExternalSessionsOptions,
  FetchSessionsOptions,
  MultiRootSessionGroup,
  PlanUpdate,
  QueuedUserMessage,
  RelatedFile,
  RelatedWorktree,
  Session,
  SessionBlock,
  SessionListPayload,
  SessionSearchHit,
  SessionType,
  SessionWindow,
  SessionWindowMeta,
  SessionWindowOptions,
  StreamEvent,
  SyncSessionResult,
  TodoItem,
  TodoUpdate,
  TokenUsage,
  ToolCall,
  ToolCallContentItem,
  ToolCallLocation,
} from "./sessionTypes";

// Session service for managing agent sessions

const commandTerminalFontSize = 12;
const commandTerminalFontFamily =
  '"Cascadia Mono", "Cascadia Code", Consolas, "Microsoft YaHei Mono", "Microsoft YaHei", "Noto Sans Mono CJK SC", monospace';

function measureCommandTerminalCellWidth(): number {
  if (typeof document === "undefined") return 7.25;
  const probe = document.createElement("span");
  probe.textContent = "mmmmmmmmmm";
  probe.style.position = "fixed";
  probe.style.left = "-9999px";
  probe.style.top = "0";
  probe.style.visibility = "hidden";
  probe.style.pointerEvents = "none";
  probe.style.whiteSpace = "pre";
  probe.style.fontFamily = commandTerminalFontFamily;
  probe.style.fontSize = `${commandTerminalFontSize}px`;
  document.body.appendChild(probe);
  const width = probe.getBoundingClientRect().width / 10;
  probe.remove();
  return width > 0 ? width : 7.25;
}

/**
 * 聊天消息窗口尺寸（首屏拉取条数 / 上翻步长）。会话窗口化的唯一尺寸来源：
 * SessionViewer 的 init `latest:`、首帧种子截断、loadMore 步长、targetSeq 取窗步长，
 * 以及 App 的 restoreActiveSession 都引此常量，避免散落的 magic number。
 * 服务端 SessionWindowMeta 的默认值/上限（50/200）是安全网，客户端始终显式传值。
 *
 * 2026-10-07：20 → 8。首屏载荷的 87% 是 exchange_aux（工具卡），而工具卡按 seq
 * 走、最重的几个 seq 决定载荷大小 —— 窗口缩小直接砍「打开会话」的载荷。代价是
 * 上翻同样历史需要更多次 loadMore，但打开会话是高频路径、翻历史是低频路径。
 *
 * CUSTOM(G-AT): 上游是 20。改回 20 等于把首屏载荷放大一倍以上。
 */
export const SESSION_WINDOW_SIZE = 8;

class SessionService {
  readonly dispatcher = new SessionDispatcher(() => this.nodeId);
  private readonly connection: SessionConnectionManager;
  private contextCache = new Map<string, { selectionKey: string }>();
  // 窗口拉取的 in-flight 去重：打开会话时 restoreActiveSession 与 SessionViewer init
  // 会各发一次 ?latest=20，并发时谁也拿不到对方的 ETag，两次都拉全量（实测日志里
  // 同一秒两个 200）。这里把同参数的并发调用合并成同一个 promise。
  private windowInflight = new Map<string, Promise<SessionWindow | null>>();
  private readonly clientId = this.generateClientId();

  constructor() {
    this.connection = new SessionConnectionManager(this.clientId, this.contextCache, {
      emit: (event) => this.dispatcher.emit(event),
      emitDecrypted: (type, sessionKey, payload, msg) => this.dispatcher.emitDecrypted(type, sessionKey, payload, msg),
    });
  }

  private generateClientId(): string {
    return generateClientId();
  }

  createRequestId(prefix = "msg"): string {
    return createRequestId(prefix);
  }

  connect(rootId: string, nodeId?: string) {
    this.connection.connect(rootId, nodeId);
  }

  disconnect() {
    this.connection.disconnect();
  }

  get nodeId(): string | null {
    return this.connection.nodeId;
  }

  // ── 消息收发方法：实现已搬进 SessionConnectionManager ──────
  // 保留类方法是为了不改调用方 —— hooks 里全是 `sessionService.sendMessage(...)`。

  async sendMessage(
    rootId: string,
    sessionKey: string | undefined,
    content: string,
    type: SessionType,
    agent: string,
    model?: string,
    agentMode?: string,
    effort?: string,
    fastService?: string,
    context?: Record<string, unknown>,
    shell?: string,
    requestId = this.createRequestId("msg"),
    newSessionWorktree?: {
      create: boolean;
      branchMode: "new" | "existing";
      branch?: string;
    },
  ): Promise<boolean> {
    return this.connection.sendMessage(rootId, sessionKey, content, type, agent, model, agentMode, effort, fastService, context, shell, requestId, newSessionWorktree);
  }

  async setPlanMode(
    rootId: string,
    sessionKey: string,
    enabled: boolean,
    requestId = this.createRequestId("plan"),
  ): Promise<boolean> {
    return this.connection.setPlanMode(rootId, sessionKey, enabled, requestId);
  }

  async runSlashCommand(
    rootId: string,
    sessionKey: string,
    command: string,
    agent: string,
    model?: string,
    agentMode?: string,
    effort?: string,
    fastService?: string,
    requestId = this.createRequestId("slash"),
  ): Promise<boolean> {
    return this.connection.runSlashCommand(rootId, sessionKey, command, agent, model, agentMode, effort, fastService, requestId);
  }

  async cancelMessage(rootId: string, sessionKey: string): Promise<boolean> {
    return this.connection.cancelMessage(rootId, sessionKey);
  }

  async removeQueuedMessage(rootId: string, sessionKey: string, queueId: string): Promise<boolean> {
    return this.connection.removeQueuedMessage(rootId, sessionKey, queueId);
  }

  async updateQueuedMessage(rootId: string, sessionKey: string, queueId: string, content: string): Promise<boolean> {
    return this.connection.updateQueuedMessage(rootId, sessionKey, queueId, content);
  }

  async sendQueuedMessageNow(rootId: string, sessionKey: string, queueId: string): Promise<boolean> {
    return this.connection.sendQueuedMessageNow(rootId, sessionKey, queueId);
  }

  async answerQuestion(
    rootId: string,
    sessionKey: string,
    agent: string | undefined,
    toolUseId: string,
    answers: Record<string, string>,
  ): Promise<boolean> {
    return this.connection.answerQuestion(rootId, sessionKey, agent, toolUseId, answers);
  }

  async markSessionReady(rootId: string, sessionKey: string): Promise<boolean> {
    return this.connection.markSessionReady(rootId, sessionKey);
  }

  /**
   * 窗口化拉取单段会话数据（方案 B）。before_seq/limit 取历史窗口，
   * latest 取尾部窗口；互斥由后端校验。透传 nodeId（缺省按 root 解析）。
   * 响应兼容顶层 `window_meta` 或嵌入 `session.window_meta`。
   */
  async getSessionWindow(
    rootId: string,
    sessionKey: string,
    opts?: SessionWindowOptions,
  ): Promise<SessionWindow | null> {
    try {
      const nodeId = opts?.nodeId || getRootNodeId(rootId);
      const params = new URLSearchParams({ root: rootId });
      if (typeof opts?.beforeSeq === "number" && opts.beforeSeq > 0) {
        params.set("before_seq", String(opts.beforeSeq));
      }
      if (typeof opts?.latest === "number" && opts.latest > 0) {
        params.set("latest", String(opts.latest));
      }
      const limit = Number(opts?.limit || 0);
      if (limit > 0) {
        params.set("limit", String(limit));
      }
      // CUSTOM(G-AT): 上游没有这层去重，同一次打开会话的两个并发调用会把几百 KB 的
      // 窗口载荷各传一遍（实测每个 sessionKey 在 ?latest=N 日志里恰好出现两次）。
      // in-flight 去重：同参数的并发窗口拉取合并成一次请求。
      // 键含 nodeId —— 同名项目跨节点是两个不同的请求，不能互相顶掉。
      const inflightKey = `${nodeId}::${rootId}::${sessionKey}::${opts?.beforeSeq || 0}::${opts?.latest || 0}::${limit}`;
      const existing = this.windowInflight.get(inflightKey);
      if (existing) {
        return existing;
      }
      const promise = (async (): Promise<SessionWindow | null> => {
        const raw = await protectedJSON<any>(
          appURL(`/api/sessions/${encodeURIComponent(sessionKey)}`, params, nodeId),
        );
        const session = (raw?.session as Session | any) || (raw as Session | any);
        const meta = (raw?.window_meta || session?.window_meta) as SessionWindowMeta;
        if (!meta) {
          console.error("[Session] getSessionWindow: missing window_meta", raw);
          return null;
        }
        return { session, meta, raw };
      })();
      this.windowInflight.set(inflightKey, promise);
      try {
        return await promise;
      } finally {
        this.windowInflight.delete(inflightKey);
      }
    } catch (err) {
      console.error("[Session] Failed to get session window:", err);
      return null;
    }
  }

  // 显式别名：现有 getSession 的增量语义（?seq=N 取 seq>N），供流式尾部追加复用。
  async getSessionIncrement(
    rootId: string,
    sessionKey: string,
    seq?: number,
    nodeId?: string,
  ): Promise<Session | null> {
    return this.getSession(rootId, sessionKey, seq, nodeId);
  }

  // ── 以下方法只是转发：实现已搬进 sessionApi.ts / sessionContext.ts ──────
  // （2026-10-07）。保留类方法是为了不改调用方 —— App.tsx / hooks 里全是
  // `sessionService.fetchSessions(...)`，改成自由函数会牵动十几处。
  // 签名逐字照搬搬迁前的版本，调用方感知不到这次重构。

  async fetchSessions(
    rootId: string,
    options?: FetchSessionsOptions & { nodeId?: string },
  ): Promise<SessionListPayload> {
    return fetchSessions(rootId, options);
  }

  async fetchMultiRootSessions(limitPerRoot = 6, nodeId?: string): Promise<MultiRootSessionGroup[]> {
    return fetchMultiRootSessions(limitPerRoot, nodeId);
  }

  async fetchChildSessions(
    rootId: string,
    parentSessionKey: string,
    options?: { beforeTime?: string; limit?: number; nodeId?: string },
  ): Promise<Session[]> {
    return fetchChildSessions(rootId, parentSessionKey, options);
  }

  async searchSessions(
    rootId: string,
    query: string,
    limit?: number,
    options?: { multiRoot?: boolean; nodeId?: string },
  ): Promise<SessionSearchHit[]> {
    return searchSessions(rootId, query, limit, options);
  }

  async getSession(
    rootId: string,
    sessionKey: string,
    seq?: number,
    nodeId?: string,
  ): Promise<Session | null> {
    return getSession(rootId, sessionKey, seq, nodeId);
  }

  async syncExternalSession(
    rootId: string,
    sessionKey: string,
    seq?: number,
    nodeId?: string,
  ): Promise<Session | null> {
    return syncExternalSession(rootId, sessionKey, seq, nodeId);
  }

  async getToolCall(
    rootId: string,
    sessionKey: string,
    callId: string,
    nodeId?: string,
  ): Promise<ToolCall | null> {
    return getToolCall(rootId, sessionKey, callId, nodeId);
  }

  async getSessionRelatedFiles(
    rootId: string,
    sessionKey: string,
    nodeId?: string,
  ): Promise<RelatedFile[]> {
    return getSessionRelatedFiles(rootId, sessionKey, nodeId);
  }

  async removeSessionRelatedFile(
    rootId: string,
    sessionKey: string,
    path: string,
    head = "",
    repoPath = "",
    repoKind = "",
    nodeId?: string,
  ): Promise<boolean> {
    return removeSessionRelatedFile(rootId, sessionKey, path, head, repoPath, repoKind, nodeId);
  }

  async deleteSession(rootId: string, sessionKey: string, nodeId?: string): Promise<boolean> {
    return deleteSession(rootId, sessionKey, nodeId);
  }

  async renameSession(
    rootId: string,
    sessionKey: string,
    name: string,
    nodeId?: string,
  ): Promise<Session | null> {
    return renameSession(rootId, sessionKey, name, nodeId);
  }

  async setSessionArchived(
    rootId: string,
    sessionKey: string,
    archived: boolean,
    nodeId?: string,
  ): Promise<Session | null> {
    return setSessionArchived(rootId, sessionKey, archived, nodeId);
  }

  async forkSession(
    rootId: string,
    sessionKey: string,
    seq: number,
    nodeId?: string,
  ): Promise<{ session_key: string; session?: Session } | null> {
    return forkSession(rootId, sessionKey, seq, nodeId);
  }

  async fetchExternalSessions(
    rootId: string,
    agent: string,
    options?: FetchExternalSessionsOptions & { nodeId?: string },
  ): Promise<Session[]> {
    return fetchExternalSessions(rootId, agent, options);
  }

  async importExternalSession(
    rootId: string,
    agent: string,
    agentSessionId: string,
    nodeId?: string,
  ): Promise<{ session_key: string } | null> {
    return importExternalSession(rootId, agent, agentSessionId, nodeId);
  }

  async importExternalSessionsBatch(
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
    return importExternalSessionsBatch(rootId, agent, agentSessionIds, nodeId);
  }

  /**
   * selection 去重：按 file+行号+文本判等，连续同选区的消息省掉 selection。
   * 实现见 sessionContext.ts —— 这两个函数没有连接状态，留在类里只是为了不散调用方。
   */
  private compactContext(
    sessionKey: string | undefined,
    context?: Record<string, unknown>,
  ): Record<string, unknown> | undefined {
    return compactContext(this.contextCache, sessionKey, context);
  }

}

export const sessionService = new SessionService();
