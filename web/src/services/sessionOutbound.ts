import type { SessionType } from "./sessionTypes";

// WebSocket 出站帧的**构造**（从 services/session.ts 的 SessionService 抽出，2026-10-07）。
//
// 这一簇是纯函数：给参数、返回帧，不碰连接状态、不发请求、不做簿记。
// 「什么时候能发」由 session.ts 的 sendWSMessage 判，「发出去之后记什么」由调用方管。
// 分开的理由：帧的形状（字段名、可选性、默认值）是协议契约，值得单独读、单独审；
// 它此前混在 244 行方法体里，与 socket 守卫、pendingMessages 调用交错。
//
// 约定：校验失败返回 `null`（调用方直接 `return false`）。**不含** socket 状态判断 ——
// 那件事只有一处判据（sendWSMessage），不过它在调用方仍有承重作用，见 session.ts。

export type WorktreeCreateInput = {
  create: boolean;
  branchMode: "new" | "existing";
  branch?: string;
};

export type SessionMessageInput = {
  requestId: string;
  rootId: string;
  sessionKey?: string;
  content: string;
  type: SessionType;
  agent: string;
  model?: string;
  agentMode?: string;
  effort?: string;
  fastService?: string;
  shell?: string;
  /** 已由调用方 compact 过（selection 去重按会话缓存，属于 SessionService 的状态）。 */
  context?: Record<string, unknown>;
  /** 命令类型才需要，用于让服务端按列数排版。**调用方**负责只在 `type === "command"` 时算它
   *  （测宽要走 DOM），这里只决定它进不进帧。 */
  terminalCols?: number;
  newSessionWorktree?: WorktreeCreateInput;
};

export function buildSessionMessage(input: SessionMessageInput): Record<string, unknown> {
  const {
    requestId,
    rootId,
    sessionKey,
    content,
    type,
    agent,
    model,
    agentMode,
    effort,
    fastService,
    shell,
    context,
    terminalCols,
    newSessionWorktree,
  } = input;
  return {
    id: requestId,
    type: "session.message",
    payload: {
      root_id: rootId,
      session_key: sessionKey || undefined,
      content,
      type,
      agent,
      model,
      agent_mode: agentMode,
      effort,
      fast_service: fastService,
      shell,
      terminal_cols: type === "command" ? terminalCols : undefined,
      create_worktree: !sessionKey && newSessionWorktree?.create === true,
      worktree_branch_mode: newSessionWorktree?.branchMode,
      worktree_branch: newSessionWorktree?.branch || "",
      context,
    },
  };
}

export function buildPlanModeMessage(
  rootId: string,
  sessionKey: string,
  enabled: boolean,
  requestId: string,
): Record<string, unknown> | null {
  if (!rootId || !sessionKey) return null;
  return {
    id: requestId,
    type: "session.plan_mode.set",
    payload: {
      root_id: rootId,
      session_key: sessionKey,
      enabled,
    },
  };
}

export type SlashCommandInput = {
  requestId: string;
  rootId: string;
  sessionKey: string;
  command: string;
  agent: string;
  model?: string;
  agentMode?: string;
  effort?: string;
  fastService?: string;
};

export function buildSlashCommandMessage(
  input: SlashCommandInput,
): Record<string, unknown> | null {
  const { requestId, rootId, sessionKey, command, agent, model, agentMode, effort, fastService } =
    input;
  if (!rootId || !sessionKey || !command || !agent) return null;
  return {
    id: requestId,
    type: "session.slash_command.run",
    payload: {
      root_id: rootId,
      session_key: sessionKey,
      command,
      agent,
      model,
      agent_mode: agentMode,
      effort,
      fast_service: fastService,
    },
  };
}

export function buildCancelMessage(
  rootId: string,
  sessionKey: string,
  requestId: string,
): Record<string, unknown> | null {
  if (!rootId || !sessionKey) return null;
  return {
    id: requestId,
    type: "session.cancel",
    payload: {
      root_id: rootId,
      session_key: sessionKey,
    },
  };
}

export function buildQueueRemoveMessage(
  rootId: string,
  sessionKey: string,
  queueId: string,
  requestId: string,
): Record<string, unknown> | null {
  if (!rootId || !sessionKey || !queueId) return null;
  return {
    id: requestId,
    type: "session.queue.remove",
    payload: {
      root_id: rootId,
      session_key: sessionKey,
      queue_id: queueId,
    },
  };
}

export function buildQueueUpdateMessage(
  rootId: string,
  sessionKey: string,
  queueId: string,
  content: string,
  requestId: string,
): Record<string, unknown> | null {
  if (!rootId || !sessionKey || !queueId || !content.trim()) return null;
  return {
    id: requestId,
    type: "session.queue.update",
    payload: {
      root_id: rootId,
      session_key: sessionKey,
      queue_id: queueId,
      content,
    },
  };
}

export function buildQueueSendNowMessage(
  rootId: string,
  sessionKey: string,
  queueId: string,
  requestId: string,
): Record<string, unknown> | null {
  if (!rootId || !sessionKey || !queueId) return null;
  return {
    id: requestId,
    type: "session.queue.send_now",
    payload: {
      root_id: rootId,
      session_key: sessionKey,
      queue_id: queueId,
    },
  };
}

export function buildAnswerQuestionMessage(
  rootId: string,
  sessionKey: string,
  agent: string | undefined,
  toolUseId: string,
  answers: Record<string, string>,
  requestId: string,
): Record<string, unknown> | null {
  if (!rootId || !sessionKey || !toolUseId) return null;
  return {
    id: requestId,
    type: "session.answer_question",
    payload: {
      root_id: rootId,
      session_key: sessionKey,
      agent,
      tool_use_id: toolUseId,
      answers,
    },
  };
}

export function buildSessionReadyMessage(
  rootId: string,
  sessionKey: string,
  requestId: string,
): Record<string, unknown> | null {
  if (!rootId || !sessionKey) return null;
  return {
    id: requestId,
    type: "session.ready",
    payload: {
      root_id: rootId,
      session_key: sessionKey,
    },
  };
}
