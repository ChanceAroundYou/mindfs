import type { Session } from "../services/session";
import { isPlanCommand } from "../shared/planCommand";
import { normalizeMode, type SessionMode } from "./appSession";
import { resolveLockedSessionKey } from "../services/session/sessionLock";
import { resolveInputSession } from "../services/session/inputSession";
import type { SendMessageParams } from "./useSendMessageTypes";

type Params = Pick<SendMessageParams,
  | "currentRootIdRef" | "selectedSessionRef" | "boundSessionByRootRef"
  | "sessionCacheRef" | "currentSessionRef" | "pendingPlanMode"
  | "resolvePendingForSession" | "scopedRootKey" | "rootSessionKey"
  | "updateSessionAgentForKey" | "setBoundSessionForRoot"
  | "setDrawerSessionForRoot" | "interactionModeRef"
>;

/**
 * 输入框这一轮「没有可发送目标」的显式原因。
 *
 * 存在意义是**不许静默**：`pending-*` 草稿还没拿到真键时，旧实现直接 `return null`
 * （消息消失、界面无反应）或干脆新建一条会话（连续两条消息 = 两个会话）。
 */
export type SendTargetBlocked = { blocked: "draft_key_missing"; rootId: string };

type Target = {
  activeRoot: string;
  sendSessionKey: string | null | undefined;
  session: Session | null;
  effectiveMode: SessionMode;
  effectiveAgent: string;
  effectiveModel: string;
  effectiveAgentMode: string;
  effectiveEffort: string;
  effectiveFastService: "" | "on" | "off";
  effectiveShell: string;
  transientSlashCommand: string;
  isQueueSend: boolean;
};

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

export function prepareSendMessageTarget(
  params: Params,
  input: Input,
): Target | SendTargetBlocked | null {
  const {
    currentRootIdRef, selectedSessionRef, boundSessionByRootRef,
    sessionCacheRef, currentSessionRef, pendingPlanMode,
    resolvePendingForSession, scopedRootKey, rootSessionKey,
    updateSessionAgentForKey, setBoundSessionForRoot, setDrawerSessionForRoot,
    interactionModeRef,
  } = params;
  const { message, mode, agent, model, agentMode, effort, fastService, shell, newSessionName } = input;
  const activeRoot = currentRootIdRef.current;
  if (!activeRoot) return null;
  const selected = selectedSessionRef.current;
  const selectedKey = selected?.key || selected?.session_key;
  const rawBoundKey = boundSessionByRootRef.current[scopedRootKey(activeRoot)] || null;
  const resolvedBoundKey = resolveLockedSessionKey(rawBoundKey);
  // `pending-*` 是「新会话已发、还没拿到服务端真键」的草稿。它既不是可续接的会话，
  // 也不能当成「没有会话」—— 后者会让紧接着的第二条消息再开一个会话。
  if (!resolvedBoundKey && rawBoundKey?.startsWith("pending-")) {
    return { blocked: "draft_key_missing", rootId: activeRoot };
  }
  let sendSessionKey: string | null | undefined = resolvedBoundKey;
  const resolveSessionForKey = (key: string): Session | null => {
    const cached = sessionCacheRef.current[rootSessionKey(activeRoot, key)];
    if (cached) return cached;
    const current = currentSessionRef.current;
    if (current?.key === key) return current as Session;
    if (selectedKey === key) return { ...(selected as any), key } as Session;
    return null;
  };
  let session: Session | null = sendSessionKey ? resolveSessionForKey(sendSessionKey) : null;
  // 兜底：绑定缺失或指向一条解析不到的会话时，续接**主面板正在显示**的那条。
  // 只见于绑定表丢失/作用域键漂移（内存表随节点键变化、刷新后未重建），
  // 而用户看到的转录还在屏幕上 —— 此时开新会话就是「上下文凭空丢失」。
  // 抽屉持有输入时不做这个回落：那里的空抽屉语义是「开新会话」。
  if (!session && interactionModeRef.current !== "drawer") {
    const displayed = resolveInputSession(activeRoot, false, selected, null);
    const displayedKey = resolveLockedSessionKey(displayed?.key || displayed?.session_key);
    if (displayedKey && displayedKey !== sendSessionKey) {
      const displayedSession = resolveSessionForKey(displayedKey);
      if (displayedSession) {
        console.warn("[session/send] bound_missing fallback_displayed", {
          rootId: activeRoot,
          bound: rawBoundKey,
          displayedKey,
        });
        sendSessionKey = displayedKey;
        session = displayedSession;
      }
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
      if (sendSessionKey) {
        // 绑定是真键、却连显示中的那条都解析不出会话对象（缓存被清而绑定残留）。
        // 保持既有行为（新建），但必须留痕：这条路径一旦静默，用户看到的就是
        // 「明明在看这个会话，发出去却开了新的」。
        console.warn("[session/send] bound_unresolvable fallback_new", {
          rootId: activeRoot,
          bound: sendSessionKey,
          displayable: selectedKey || null,
        });
      }
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
