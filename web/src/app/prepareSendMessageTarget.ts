import type { Session } from "../services/session";
import { isPlanCommand } from "../shared/planCommand";
import { normalizeMode, type SessionMode } from "./appSession";
import { resolveLockedSessionKey } from "../services/session/sessionLock";
import type { SendMessageParams } from "./useSendMessageTypes";

type Params = Pick<SendMessageParams,
  | "currentRootIdRef" | "selectedSessionRef" | "boundSessionByRootRef"
  | "sessionCacheRef" | "currentSessionRef" | "pendingPlanMode"
  | "resolvePendingForSession" | "scopedRootKey" | "rootSessionKey"
  | "updateSessionAgentForKey" | "setBoundSessionForRoot"
  | "setDrawerSessionForRoot"
>;

type Input = {
  message: string;
  mode: SessionMode;
  agent: string;
  model?: string;
  agentMode?: string;
  effort?: string;
  fastService?: "" | "on" | "off";
  shell?: string;
  newSessionName: string;
};

export function prepareSendMessageTarget(params: Params, input: Input) {
  const {
    currentRootIdRef, selectedSessionRef, boundSessionByRootRef,
    sessionCacheRef, currentSessionRef, pendingPlanMode,
    resolvePendingForSession, scopedRootKey, rootSessionKey,
    updateSessionAgentForKey, setBoundSessionForRoot, setDrawerSessionForRoot,
  } = params;
  const { message, mode, agent, model, agentMode, effort, fastService, shell, newSessionName } = input;
  const activeRoot = currentRootIdRef.current;
  if (!activeRoot) return null;
  const selected = selectedSessionRef.current;
  const selectedKey = selected?.key || selected?.session_key;
  const currentBoundSessionKey =
    boundSessionByRootRef.current[scopedRootKey(activeRoot)] || null;
  const resolvedBoundKey = resolveLockedSessionKey(currentBoundSessionKey);
  let sendSessionKey: string | null | undefined = resolvedBoundKey;
  // The server must assign a real key before this draft can receive more input.
  if (sendSessionKey?.startsWith("pending-")) return null;
  let session: Session | null = null;
  if (sendSessionKey) {
    session =
      sessionCacheRef.current[rootSessionKey(activeRoot, sendSessionKey)];
    if (!session) {
      const current = currentSessionRef.current;
      if (current?.key === sendSessionKey) {
        session = current as Session;
      }
    }
    if (!session && selectedKey === sendSessionKey) {
      session = { ...(selected as any), key: sendSessionKey } as Session;
    }
  }
  let effectiveMode = mode,
    effectiveAgent = agent,
    effectiveModel = model || "",
    effectiveAgentMode = agentMode || "",
    effectiveEffort = effort || "",
    effectiveFastService = (fastService || "") as "" | "on" | "off",
    effectiveShell = shell || "";
  const messageRequestsPlanMode = isPlanCommand(message);
  const normalizedMessage = message.trim().toLowerCase();
  const messageRequestsStatus = normalizedMessage === "/status";
  const messageRequestsLogin = normalizedMessage === "/login";
  const transientSlashCommand = messageRequestsStatus
    ? "status"
    : messageRequestsLogin
      ? "login"
      : "";
  const isQueueSend =
    !!sendSessionKey &&
    !!session &&
    // pending 纯派生：先看 multiProjectPendingByKey，表里没有才落回会话对象上
    // 那份（服务端列表回包带的快照）。
    resolvePendingForSession(
      ((session as any).root_id as string | undefined) || currentRootIdRef.current || "",
      sendSessionKey,
      (session as any).pending === true ||
        (currentSessionRef.current?.key === sendSessionKey &&
          currentSessionRef.current?.pending === true),
    );
  if (sendSessionKey && session) {
    const targetSessionKey = sendSessionKey;
    const previousAgent = session.agent || "";
    effectiveMode = normalizeMode(session.type as any);
    effectiveAgent =
      agent ||
      previousAgent ||
      "";
    effectiveModel =
      model ||
      (effectiveAgent === previousAgent ? session.model || "" : "");
    effectiveAgentMode =
      agentMode ||
      (effectiveAgent === previousAgent ? (session as any).mode || "" : "");
    effectiveEffort =
      effort ||
      (effectiveAgent === previousAgent ? (session as any).effort || "" : "");
    effectiveFastService =
      ((fastService || "") as "" | "on" | "off") ||
      (effectiveAgent === previousAgent
        ? (((session as any).fast_service || "") as "" | "on" | "off")
        : "");
    effectiveShell =
      effectiveMode === "command"
        ? (shell ||
            (session as any).shell ||
            "")
        : "";
    updateSessionAgentForKey(
      activeRoot,
      targetSessionKey,
      effectiveAgent,
      effectiveModel,
      effectiveAgentMode,
      effectiveEffort,
      effectiveFastService,
    );
    session = {
      ...(session as any),
      agent: effectiveAgent,
      model: effectiveModel,
      mode: effectiveAgentMode,
      effort: effectiveEffort,
      fast_service: effectiveFastService,
      shell: effectiveShell,
    } as Session;
    setBoundSessionForRoot(activeRoot, targetSessionKey);
    if (!isQueueSend) {
      // pending 不再写在会话对象上（纯派生自 multiProjectPendingByKey，见下方
      // setMultiProjectSessionPending）；这里只把抽屉指向这条会话。
      setDrawerSessionForRoot(activeRoot, session as Session);
    }
  } else {
    if (transientSlashCommand) {
      sendSessionKey = `transient-${Date.now()}`;
      session = null;
    } else {
      sendSessionKey = undefined;
      const tempKey = `pending-${Date.now()}`;
      session = {
        key: tempKey,
        type: mode,
        agent,
        model: effectiveModel,
        mode: effectiveAgentMode,
        effort: effectiveEffort,
        fast_service: effectiveFastService,
        shell: effectiveShell,
        plan_mode: pendingPlanMode || messageRequestsPlanMode,
        name: newSessionName,
      } as any;
      setBoundSessionForRoot(activeRoot, tempKey);
    }
  }
  return {
    activeRoot,
    sendSessionKey,
    session,
    effectiveMode,
    effectiveAgent,
    effectiveModel,
    effectiveAgentMode,
    effectiveEffort,
    effectiveFastService,
    effectiveShell,
    transientSlashCommand,
    isQueueSend,
  };
}
