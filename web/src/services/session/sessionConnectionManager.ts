/**
 * SessionConnectionManager — WebSocket 连接生命周期 + 消息收发。
 *
 * 从 session.ts 拆出（2026-10-10）：原文件 971 行，WS 状态与消息处理紧密耦合，
 * 拆出后 SessionService 只保留高层编排（API 转发、窗口拉取、dispatcher 桥接）。
 *
 * CUSTOM(G-BA)：上游没有这个拆分。合上游时若 session.ts 被整体覆盖，
 * 需要把本文件的内容合回去（或重新拆分），否则 WS 连接管理会丢失。
 */
import { wsURL } from "../net/base";
import { createRequestId } from "./sessionIdentifiers";
import type { PendingMessage, SessionServiceEvent } from "./sessionTypes";
import { SessionConnectionOutbound } from "./sessionConnectionOutbound";

export interface SessionConnectionManagerHandlers {
  emit: (event: SessionServiceEvent) => void;
  emitDecrypted: (type: string, sessionKey: string, payload: any, msg: any) => void;
}

export class SessionConnectionManager {
  private ws: WebSocket | null = null;
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
  nodeId: string | null = null;
  private hasConnected = false;
  private readonly maxReconnectDelayMs = 30000;
  private readonly fastReconnectDelayMs = 1000;
  private readonly fastReconnectWindowMs = 10000;
  private readonly connectTimeoutMs = 15000;
  private readonly probeTimeoutMs = 8000;
  private readonly lifecycleCheckDelayMs = 750;
  private readonly maxConsecutiveProbeFailures = 2;
  private readonly reconnectWatchdogMs = 3000;

  private readonly clientId: string;
  private readonly contextCache: Map<string, { selectionKey: string }>;
  private readonly handlers: SessionConnectionManagerHandlers;
  private readonly outbound: SessionConnectionOutbound;

  constructor(
    clientId: string,
    contextCache: Map<string, { selectionKey: string }>,
    handlers: SessionConnectionManagerHandlers,
  ) {
    this.clientId = clientId;
    this.contextCache = contextCache;
    this.handlers = handlers;
    this.outbound = new SessionConnectionOutbound(
      () => Boolean(this.ws && this.ws.readyState === WebSocket.OPEN),
      (message) => this.sendWSMessage(message),
      () => this.ws?.readyState ?? null,
      this.pendingMessages,
      this.contextCache,
    );
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

  private buildWSUrl(nodeId?: string): string {
    const params = new URLSearchParams({ client_id: this.clientId });
    if (nodeId) params.set("node_id", nodeId);
    return wsURL("/ws", params, nodeId);
  }

  connect(rootId: string, nodeId?: string) {
    const nextNodeId = nodeId !== undefined ? (nodeId || null) : this.nodeId;
    const nodeIdChanged = nextNodeId !== this.nodeId;
    this.rootId = rootId;
    if (nodeId !== undefined) this.nodeId = nextNodeId;
    if (this.ws?.readyState === WebSocket.OPEN) {
      if (!nodeIdChanged) {
        this.handlers.emit({ type: this.hasConnected ? "ws.reconnected" : "ws.connected" });
        this.hasConnected = true;
        return;
      }
      this.clearReconnectTimer();
      this.closeSocket();
      this.handlers.emit({ type: this.hasConnected ? "ws.reconnecting" : "ws.connecting" });
      void this.openSocket();
      return;
    }
    if (this.openingSocket || this.ws?.readyState === WebSocket.CONNECTING) {
      if (nodeIdChanged) {
        this.clearReconnectTimer();
        this.closeSocket();
        this.handlers.emit({ type: this.hasConnected ? "ws.reconnecting" : "ws.connecting" });
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
      this.handlers.emit({ type: this.hasConnected ? "ws.reconnecting" : "ws.connecting" });
      return;
    }

    this.clearReconnectTimer();
    this.closeSocket();
    this.handlers.emit({ type: this.hasConnected ? "ws.reconnecting" : "ws.connecting" });

    void this.openSocket();
  }

  private async openSocket() {
    if (this.openingSocket) {
      return;
    }
    this.openingSocket = true;
    let target = "";
    try {
      target = this.buildWSUrl(this.nodeId || undefined);
    } catch (err) {
      this.openingSocket = false;
      console.error("[Session] Failed to prepare WebSocket proof:", err);
      this.handlers.emit({ type: "ws.closed", payload: { code: 0, reason: "ws_connect_failed", was_clean: false } });
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
        this.handlers.emit({ type: "ws.reconnected" });
      } else {
        this.handlers.emit({ type: "ws.connected" });
      }
      this.hasConnected = true;
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
      this.handlers.emit({
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
    const probeId = createRequestId("ping");
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
    this.handlers.emitDecrypted(type, sessionKey, payload, msg);
  }

  private parseWSMessage(raw: unknown): any | null {
    if (typeof raw !== "string") {
      return null;
    }
    return JSON.parse(raw);
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

  private async sendWSMessage(
    message: Record<string, unknown>,
  ): Promise<boolean> {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      return false;
    }
    let serialized = JSON.stringify(message);
    this.ws.send(serialized);
    return true;
  }

  sendMessage(...args: Parameters<SessionConnectionOutbound["sendMessage"]>) {
    return this.outbound.sendMessage(...args);
  }

  setPlanMode(...args: Parameters<SessionConnectionOutbound["setPlanMode"]>) {
    return this.outbound.setPlanMode(...args);
  }

  runSlashCommand(...args: Parameters<SessionConnectionOutbound["runSlashCommand"]>) {
    return this.outbound.runSlashCommand(...args);
  }

  cancelMessage(...args: Parameters<SessionConnectionOutbound["cancelMessage"]>) {
    return this.outbound.cancelMessage(...args);
  }

  removeQueuedMessage(...args: Parameters<SessionConnectionOutbound["removeQueuedMessage"]>) {
    return this.outbound.removeQueuedMessage(...args);
  }

  updateQueuedMessage(...args: Parameters<SessionConnectionOutbound["updateQueuedMessage"]>) {
    return this.outbound.updateQueuedMessage(...args);
  }

  sendQueuedMessageNow(...args: Parameters<SessionConnectionOutbound["sendQueuedMessageNow"]>) {
    return this.outbound.sendQueuedMessageNow(...args);
  }

  answerQuestion(...args: Parameters<SessionConnectionOutbound["answerQuestion"]>) {
    return this.outbound.answerQuestion(...args);
  }

  markSessionReady(...args: Parameters<SessionConnectionOutbound["markSessionReady"]>) {
    return this.outbound.markSessionReady(...args);
  }

}
