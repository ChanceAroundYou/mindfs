import { estimateCommandTerminalCols } from "./sessionTerminalLayout";
import type { CachedSessionListRecord, CachedSessionRecord } from "./sessionCacheTypes";
import { appURL, wsURL } from "./net/base";
import { getRootNodeId } from "./net/rootNode";
import { scopeSessionKey } from "../shared/scope";
import { protectedFetch, protectedJSON, withNodeRetry } from "./net/api";
import { e2eeService } from "./net/e2ee";
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
  getCachedMultiRootSessionList,
  saveCachedMultiRootSessionList,
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
  private ws: WebSocket | null = null;
  readonly dispatcher = new SessionDispatcher(() => this.nodeId);
  private pendingMessages = new Map<string, PendingMessage>();
  private reconnectTimer: number | null = null;
  private connectTimeoutTimer: number | null = null;
  private probeTimeoutTimer: number | null = null;
  private lifecycleCheckTimer: number | null = null;
  private activeProbeId: string | null = null;
  // 连续 probe 超时计数：达到 2 次才强断重连；收到任何消息即清零（handleMessage 顶部）。
  private consecutiveProbeFailures = 0;
  private connectingStartedAt = 0;
  private openingSocket = false;
  private reconnectDelayMs = 1000;
  private fastReconnectUntil = 0;
  private rootId: string | null = null;
  private nodeId: string | null = null;
  private hasConnected = false;
  private readonly clientId = this.generateClientId();
  private readonly maxReconnectDelayMs = 30000;
  private readonly fastReconnectDelayMs = 1000;
  private readonly fastReconnectWindowMs = 10000;
  private readonly connectTimeoutMs = 15000;
  private readonly probeTimeoutMs = 8000;
  private readonly lifecycleCheckDelayMs = 750;
  private readonly maxConsecutiveProbeFailures = 2;
  private readonly reconnectWatchdogMs = 3000;
  private contextCache = new Map<string, { selectionKey: string }>();
  // 窗口拉取的 in-flight 去重：打开会话时 restoreActiveSession 与 SessionViewer init
  // 会各发一次 ?latest=20，并发时谁也拿不到对方的 ETag，两次都拉全量（实测日志里
  // 同一秒两个 200）。这里把同参数的并发调用合并成同一个 promise。
  private windowInflight = new Map<string, Promise<SessionWindow | null>>();

  constructor() {
    e2eeService.setClientId(this.clientId);
    if (typeof window !== "undefined") {
      window.addEventListener("online", () => this.scheduleLifecycleCheck());
      window.addEventListener("pageshow", () => this.scheduleLifecycleCheck());
      window.addEventListener("focus", () => this.scheduleLifecycleCheck());
      window.setInterval(() => this.ensureReconnectLoop(), this.reconnectWatchdogMs);
    }
    if (typeof document !== "undefined") {
      document.addEventListener("visibilitychange", () => {
        if (document.visibilityState === "visible") {
          this.scheduleLifecycleCheck();
        }
      });
    }
  }

  private generateClientId(): string {
    return generateClientId();
  }

  createRequestId(prefix = "msg"): string {
    return createRequestId(prefix);
  }

  private async buildWSUrl(nodeId?: string): Promise<string> {
    const params = new URLSearchParams({ client_id: this.clientId });
    if (nodeId) params.set("node_id", nodeId);
    const proofTarget = wsURL("/ws", params, nodeId);
    if (e2eeService.isRequired()) {
      const proofParams = await e2eeService.wsProofParams("GET", proofTarget);
      for (const [key, value] of proofParams) {
        params.set(key, value);
      }
    }
    return wsURL("/ws", params, nodeId);
  }

  connect(rootId: string, nodeId?: string) {
    const nextNodeId = nodeId !== undefined ? (nodeId || null) : this.nodeId;
    const nodeIdChanged = nextNodeId !== this.nodeId;
    this.rootId = rootId;
    if (nodeId !== undefined) this.nodeId = nextNodeId;
    if (this.ws?.readyState === WebSocket.OPEN) {
      if (!nodeIdChanged) {
        this.dispatcher.emit({ type: this.hasConnected ? "ws.reconnected" : "ws.connected" });
        this.hasConnected = true;
        return;
      }
      this.clearReconnectTimer();
      this.closeSocket();
      this.dispatcher.emit({ type: this.hasConnected ? "ws.reconnecting" : "ws.connecting" });
      void this.openSocket();
      return;
    }
    if (this.openingSocket || this.ws?.readyState === WebSocket.CONNECTING) {
      if (nodeIdChanged) {
        this.clearReconnectTimer();
        this.closeSocket();
        this.dispatcher.emit({ type: this.hasConnected ? "ws.reconnecting" : "ws.connecting" });
        void this.openSocket();
        return;
      }
      if (
        this.connectingStartedAt > 0 &&
        Date.now() - this.connectingStartedAt > this.connectTimeoutMs
      ) {
        this.reconnectNow();
        return;
      }
      this.dispatcher.emit({ type: this.hasConnected ? "ws.reconnecting" : "ws.connecting" });
      return;
    }

    this.clearReconnectTimer();
    this.closeSocket();
    this.dispatcher.emit({ type: this.hasConnected ? "ws.reconnecting" : "ws.connecting" });

    void this.openSocket();
  }

  private async openSocket() {
    if (this.openingSocket) {
      return;
    }
    this.openingSocket = true;
    let target = "";
    try {
      target = await this.buildWSUrl(this.nodeId || undefined);
    } catch (err) {
      this.openingSocket = false;
      console.error("[Session] Failed to prepare WebSocket proof:", err);
      this.dispatcher.emit({ type: "ws.closed", payload: { code: 0, reason: "e2ee_proof_failed", was_clean: false } });
      this.scheduleReconnect();
      return;
    }
    if (!this.rootId || this.ws) {
      this.openingSocket = false;
      return;
    }
    const ws = new WebSocket(target);
    this.openingSocket = false;
    this.ws = ws;
    this.connectingStartedAt = Date.now();
    this.connectTimeoutTimer = window.setTimeout(() => {
      if (this.ws !== ws || ws.readyState !== WebSocket.CONNECTING) return;
      console.warn("[Session] WebSocket connect timed out, reconnecting");
      this.reconnectNow();
    }, this.connectTimeoutMs);

    ws.onopen = () => {
      if (this.ws !== ws) return;
      this.clearConnectTimeout();
      this.clearProbe();
      this.reconnectDelayMs = 1000;
      this.consecutiveProbeFailures = 0;
      if (this.hasConnected) {
        this.dispatcher.emit({ type: "ws.reconnected" });
      } else {
        this.dispatcher.emit({ type: "ws.connected" });
      }
      this.hasConnected = true;
      if (e2eeService.isRequired() && e2eeService.hasSecret()) {
        void e2eeService.ensureSession().catch((err) => {
          console.error("[Session] Failed to open E2EE session:", err);
        });
      }
      this.resendPendingMessages();
    };

    ws.onmessage = (event) => {
      if (this.ws !== ws) return;
      this.consecutiveProbeFailures = 0;
      this.clearProbe();
      void (async () => {
        try {
          const msg = await this.parseWSMessage(event.data);
          if (!msg) {
            return;
          }
          this.handleMessage(msg);
        } catch (err) {
          console.error("[Session] Failed to parse message:", err);
        }
      })();
    };

    ws.onclose = (event) => {
      if (this.ws !== ws) return;
      this.clearConnectTimeout();
      this.ws = null;
      this.dispatcher.emit({
        type: "ws.closed",
        payload: {
          code: event.code,
          reason: event.reason,
          was_clean: event.wasClean,
        },
      });
      this.fastReconnectUntil = Date.now() + this.fastReconnectWindowMs;
      this.scheduleReconnect();
    };

    ws.onerror = (err) => {
      if (this.ws !== ws) return;
      console.error("[Session] WebSocket error:", err);
    };
  }

  disconnect() {
    this.rootId = null;
    this.clearReconnectTimer();
    this.clearConnectTimeout();
    this.clearProbe();
    this.clearLifecycleCheck();
    this.consecutiveProbeFailures = 0;
    this.closeSocket();
    this.contextCache.clear();
  }

  private clearReconnectTimer() {
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  private clearConnectTimeout() {
    if (this.connectTimeoutTimer) {
      clearTimeout(this.connectTimeoutTimer);
      this.connectTimeoutTimer = null;
    }
    this.connectingStartedAt = 0;
  }

  private clearProbe() {
    if (this.probeTimeoutTimer) {
      clearTimeout(this.probeTimeoutTimer);
      this.probeTimeoutTimer = null;
    }
    this.activeProbeId = null;
  }

  private clearLifecycleCheck() {
    if (this.lifecycleCheckTimer) {
      clearTimeout(this.lifecycleCheckTimer);
      this.lifecycleCheckTimer = null;
    }
  }

  private scheduleLifecycleCheck() {
    if (!this.rootId) return;
    this.clearLifecycleCheck();
    this.lifecycleCheckTimer = window.setTimeout(() => {
      this.lifecycleCheckTimer = null;
      this.ensureConnection();
    }, this.lifecycleCheckDelayMs);
  }

  private closeSocket() {
    this.clearConnectTimeout();
    this.clearProbe();
    this.openingSocket = false;
    if (this.ws) {
      const ws = this.ws;
      this.ws = null;
      ws.onclose = null;
      ws.onerror = null;
      ws.onmessage = null;
      ws.onopen = null;
      ws.close();
    }
  }

  private ensureConnection() {
    if (!this.rootId) return;
    if (!this.ws || this.ws.readyState >= WebSocket.CLOSING) {
      this.reconnectNow();
      return;
    }
    if (this.ws.readyState === WebSocket.CONNECTING) {
      if (
        this.connectingStartedAt > 0 &&
        Date.now() - this.connectingStartedAt > this.connectTimeoutMs
      ) {
        this.reconnectNow();
      }
      return;
    }
    this.probeConnection();
  }

  private ensureReconnectLoop() {
    if (!this.rootId) return;
    if (!this.ws || this.ws.readyState >= WebSocket.CLOSING) {
      this.reconnectNow();
      return;
    }
    if (
      this.ws.readyState === WebSocket.CONNECTING &&
      this.connectingStartedAt > 0 &&
      Date.now() - this.connectingStartedAt > this.connectTimeoutMs
    ) {
      this.reconnectNow();
    }
    this.probeConnection();
  }

  private reconnectNow() {
    if (!this.rootId) return;
    const rootId = this.rootId;
    const nodeId = this.nodeId || undefined;
    this.clearReconnectTimer();
    this.clearProbe();
    this.closeSocket();
    this.reconnectDelayMs = 1000;
    this.fastReconnectUntil = Date.now() + this.fastReconnectWindowMs;
    this.connect(rootId, nodeId);
  }

  private probeConnection() {
    if (!this.rootId || !this.ws || this.ws.readyState !== WebSocket.OPEN) {
      this.reconnectNow();
      return;
    }
    if (this.activeProbeId) return;
    const probeId = this.createRequestId("ping");
    this.activeProbeId = probeId;
    void this.sendWSMessage({
      id: probeId,
      type: "ping",
      payload: {},
    }).catch((err) => {
      console.error("[Session] Failed to send probe:", err);
    });
    this.probeTimeoutTimer = window.setTimeout(() => {
      if (this.activeProbeId !== probeId) return;
      this.consecutiveProbeFailures += 1;
      const shouldReconnect =
        this.consecutiveProbeFailures >= this.maxConsecutiveProbeFailures;
      console.warn("[Session] WebSocket probe timed out", {
        consecutiveFailures: this.consecutiveProbeFailures,
        reconnecting: shouldReconnect,
      });
      this.clearProbe();
      if (shouldReconnect) {
        this.consecutiveProbeFailures = 0;
        this.reconnectNow();
        return;
      }
      this.probeConnection();
    }, this.probeTimeoutMs);
  }


  private scheduleReconnect() {
    if (!this.rootId) return;
    if (this.reconnectTimer) return;
    const isFastReconnect = Date.now() < this.fastReconnectUntil;
    const delay = isFastReconnect
      ? this.fastReconnectDelayMs
      : this.reconnectDelayMs;
    if (!isFastReconnect) {
      this.reconnectDelayMs = Math.min(
        this.reconnectDelayMs * 2,
        this.maxReconnectDelayMs,
      );
    }
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null;
      if (this.rootId) {
        this.connect(this.rootId, this.nodeId || undefined);
      }
    }, delay);
  }

  private handleMessage(msg: any) {
    // 收到任何消息（含 pong）即证明连接存活：清零 probe 失败计数并取消在途 probe 超时，
    // probe 放宽（8s+连续 2 次）才重连。journal 实证上游 2s 一票否决在手机路径上抖动严重。
    this.consecutiveProbeFailures = 0;
    if (this.activeProbeId) {
      this.clearProbe();
    }
    const type = msg.type as string;
    const payload = msg.payload || {};
    if (type === "pong") {
      return;
    }
    if (type === "e2ee.error") {
      const code = typeof payload.code === "string" ? payload.code : "";
      e2eeService.handleServerError(code);
      this.dispatcher.emit({ type, payload });
      return;
    }
    const sessionKey = payload.session_key as string;
    if (type === "session.accepted") {
      const requestId =
        typeof payload.request_id === "string"
          ? payload.request_id
          : typeof msg.id === "string"
            ? msg.id
            : "";
      if (requestId) {
        this.pendingMessages.delete(requestId);
      }
    } else if (type === "session.error" && typeof msg.id === "string") {
      this.pendingMessages.delete(msg.id);
      payload.request_id = msg.id;
    }
    this.dispatcher.emitDecrypted(type, sessionKey, payload, msg);
  }

  private async parseWSMessage(raw: unknown): Promise<any | null> {
    if (typeof raw !== "string") {
      return null;
    }
    const parsed = JSON.parse(raw);
    if (!e2eeService.isRequired() || parsed?.type === "e2ee.error") {
      return parsed;
    }
    return e2eeService.decodeWSMessage<any>(raw);
  }

  private async sendWSMessage(
    message: Record<string, unknown>,
  ): Promise<boolean> {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      return false;
    }
    let serialized = JSON.stringify(message);
    if (e2eeService.isRequired()) {
      await e2eeService.ensureSession();
      serialized = await e2eeService.encodeWSMessage(message);
    }
    this.ws.send(serialized);
    return true;
  }

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
    // 这条守卫**必须留在调用方**：守卫之后要动两处状态（contextCache 经
    // compactContext、pendingMessages），socket 已关时不该产生这两笔副作用。
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      console.warn("[session/send] blocked", {
        requestId,
        rootId,
        sessionKey: sessionKey || null,
        readyState: this.ws?.readyState ?? null,
      });
      return false;
    }

    const msg = buildSessionMessage({
      requestId,
      rootId,
      sessionKey: sessionKey || undefined,
      content,
      type,
      agent,
      model,
      agentMode,
      effort,
      fastService,
      shell,
      context: this.compactContext(sessionKey, context),
      // 只在命令类型下测宽：estimateCommandTerminalCols 会做 DOM 测量
      // （querySelectorAll + getBoundingClientRect），普通聊天消息不该付这个代价。
      terminalCols: type === "command" ? estimateCommandTerminalCols() : undefined,
      newSessionWorktree,
    });

    this.pendingMessages.set(requestId, { id: requestId, message: msg });
    return this.sendWSMessage(msg);
  }

  async setPlanMode(
    rootId: string,
    sessionKey: string,
    enabled: boolean,
    requestId = this.createRequestId("plan"),
  ): Promise<boolean> {
    const msg = buildPlanModeMessage(rootId, sessionKey, enabled, requestId);
    return msg ? this.sendWSMessage(msg) : false;
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
    // 同 sendMessage：守卫之后要写 pendingMessages，所以不能下推到 sendWSMessage。
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      return false;
    }
    const msg = buildSlashCommandMessage({
      requestId,
      rootId,
      sessionKey,
      command,
      agent,
      model,
      agentMode,
      effort,
      fastService,
    });
    if (!msg) return false;
    this.pendingMessages.set(requestId, { id: requestId, message: msg });
    return this.sendWSMessage(msg);
  }

  private resendPendingMessages() {
    // 不判 socket：sendWSMessage 会为每一条返回 false（socket 关了就没有可发的），
    // 而循环本身不改状态。
    for (const pending of this.pendingMessages.values()) {
      void this.sendWSMessage(pending.message).catch((err) => {
        console.error("[Session] Failed to resend message:", err);
      });
    }
  }

  async cancelMessage(rootId: string, sessionKey: string): Promise<boolean> {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      console.error("[Session] WebSocket not connected");
      return false;
    }
    const msg = buildCancelMessage(rootId, sessionKey, `cancel-${Date.now()}`);
    return msg ? this.sendWSMessage(msg) : false;
  }

  async removeQueuedMessage(rootId: string, sessionKey: string, queueId: string): Promise<boolean> {
    const msg = buildQueueRemoveMessage(rootId, sessionKey, queueId, `queue-remove-${Date.now()}`);
    return msg ? this.sendWSMessage(msg) : false;
  }

  async updateQueuedMessage(rootId: string, sessionKey: string, queueId: string, content: string): Promise<boolean> {
    const msg = buildQueueUpdateMessage(
      rootId,
      sessionKey,
      queueId,
      content,
      `queue-update-${Date.now()}`,
    );
    return msg ? this.sendWSMessage(msg) : false;
  }

  async sendQueuedMessageNow(rootId: string, sessionKey: string, queueId: string): Promise<boolean> {
    const msg = buildQueueSendNowMessage(
      rootId,
      sessionKey,
      queueId,
      `queue-send-now-${Date.now()}`,
    );
    return msg ? this.sendWSMessage(msg) : false;
  }

  async answerQuestion(
    rootId: string,
    sessionKey: string,
    agent: string | undefined,
    toolUseId: string,
    answers: Record<string, string>,
  ): Promise<boolean> {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      console.error("[Session] WebSocket not connected");
      return false;
    }
    const msg = buildAnswerQuestionMessage(
      rootId,
      sessionKey,
      agent,
      toolUseId,
      answers,
      this.createRequestId("answer"),
    );
    return msg ? this.sendWSMessage(msg) : false;
  }

  // 挂载会话：服务端收到后一律回一帧 `session.stream{reset:true, events}`（整轮快照），
  // 客户端先清空该会话的瞬时尾巴再照单重建。**没有 event_cursor** —— 不从客户端取进度，
  // 也就没有「客户端缓存与服务端游标必须同步」这条维持不住的不变量。
  async markSessionReady(rootId: string, sessionKey: string): Promise<boolean> {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      return false;
    }
    const msg = buildSessionReadyMessage(rootId, sessionKey, `ready-${Date.now()}`);
    if (!msg) return false;
    if (e2eeService.isRequired()) {
      await e2eeService.ensureSession();
    }
    return this.sendWSMessage(msg);
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
