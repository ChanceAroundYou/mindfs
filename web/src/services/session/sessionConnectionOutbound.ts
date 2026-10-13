import { estimateCommandTerminalCols } from "./sessionTerminalLayout";
import { createRequestId } from "./sessionIdentifiers";
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
import { compactContext } from "./sessionContext";
import type { PendingMessage, SessionType } from "./sessionTypes";

export class SessionConnectionOutbound {
  private readonly isSocketOpen: () => boolean;
  private readonly send: (message: Record<string, unknown>) => Promise<boolean>;
  private readonly readyState: () => number | null;
  private readonly pendingMessages: Map<string, PendingMessage>;
  private readonly contextCache: Map<string, { selectionKey: string }>;

  constructor(
    isSocketOpen: () => boolean,
    send: (message: Record<string, unknown>) => Promise<boolean>,
    readyState: () => number | null,
    pendingMessages: Map<string, PendingMessage>,
    contextCache: Map<string, { selectionKey: string }>,
  ) {
    this.isSocketOpen = isSocketOpen;
    this.send = send;
    this.readyState = readyState;
    this.pendingMessages = pendingMessages;
    this.contextCache = contextCache;
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
    requestId = createRequestId("msg"),
    newSessionWorktree?: {
      create: boolean;
      branchMode: "new" | "existing";
      branch?: string;
    },
  ): Promise<boolean> {
    // 这条守卫**必须留在调用方**：守卫之后要动两处状态（contextCache 经
    // compactContext、pendingMessages），socket 已关时不该产生这两笔副作用。
    if (!this.isSocketOpen()) {
      console.warn("[session/send] blocked", {
        requestId,
        rootId,
        sessionKey: sessionKey || null,
        readyState: this.readyState(),
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
      context: compactContext(this.contextCache, sessionKey, context),
      // 只在命令类型下测宽：estimateCommandTerminalCols 会做 DOM 测量
      // （querySelectorAll + getBoundingClientRect），普通聊天消息不该付这个代价。
      terminalCols: type === "command" ? estimateCommandTerminalCols() : undefined,
      newSessionWorktree,
    });

    this.pendingMessages.set(requestId, { id: requestId, message: msg });
    return this.send(msg);
  }

  async setPlanMode(
    rootId: string,
    sessionKey: string,
    enabled: boolean,
    requestId = createRequestId("plan"),
  ): Promise<boolean> {
    const msg = buildPlanModeMessage(rootId, sessionKey, enabled, requestId);
    return msg ? this.send(msg) : false;
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
    requestId = createRequestId("slash"),
  ): Promise<boolean> {
    // 同 sendMessage：守卫之后要写 pendingMessages，所以不能下推到 sendWSMessage。
    if (!this.isSocketOpen()) {
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
    return this.send(msg);
  }


  async cancelMessage(rootId: string, sessionKey: string): Promise<boolean> {
    if (!this.isSocketOpen()) {
      console.error("[Session] WebSocket not connected");
      return false;
    }
    const msg = buildCancelMessage(rootId, sessionKey, `cancel-${Date.now()}`);
    return msg ? this.send(msg) : false;
  }

  async removeQueuedMessage(rootId: string, sessionKey: string, queueId: string): Promise<boolean> {
    const msg = buildQueueRemoveMessage(rootId, sessionKey, queueId, `queue-remove-${Date.now()}`);
    return msg ? this.send(msg) : false;
  }

  async updateQueuedMessage(rootId: string, sessionKey: string, queueId: string, content: string): Promise<boolean> {
    const msg = buildQueueUpdateMessage(
      rootId,
      sessionKey,
      queueId,
      content,
      `queue-update-${Date.now()}`,
    );
    return msg ? this.send(msg) : false;
  }

  async sendQueuedMessageNow(rootId: string, sessionKey: string, queueId: string): Promise<boolean> {
    const msg = buildQueueSendNowMessage(
      rootId,
      sessionKey,
      queueId,
      `queue-send-now-${Date.now()}`,
    );
    return msg ? this.send(msg) : false;
  }

  async answerQuestion(
    rootId: string,
    sessionKey: string,
    agent: string | undefined,
    toolUseId: string,
    answers: Record<string, string>,
  ): Promise<boolean> {
    if (!this.isSocketOpen()) {
      console.error("[Session] WebSocket not connected");
      return false;
    }
    const msg = buildAnswerQuestionMessage(
      rootId,
      sessionKey,
      agent,
      toolUseId,
      answers,
      createRequestId("answer"),
    );
    return msg ? this.send(msg) : false;
  }

  // 挂载会话：服务端收到后一律回一帧 `session.stream{reset:true, events}`（整轮快照），
  // 客户端先清空该会话的瞬时尾巴再照单重建。**没有 event_cursor** —— 不从客户端取进度，
  // 也就没有「客户端缓存与服务端游标必须同步」这条维持不住的不变量。
  async markSessionReady(rootId: string, sessionKey: string): Promise<boolean> {
    if (!this.isSocketOpen()) {
      return false;
    }
    const msg = buildSessionReadyMessage(rootId, sessionKey, `ready-${Date.now()}`);
    if (!msg) return false;
    return this.send(msg);
  }
}
