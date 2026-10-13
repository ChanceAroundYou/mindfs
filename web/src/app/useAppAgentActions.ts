import { useCallback } from "react";
import { sessionService, type Session } from "../services/session";
import { reportError } from "../services/net/error";
import { mergeSessionItems } from "../services/session/sessionListMerge";
import { toSessionItem, type SessionItem, type SessionMode } from "./appSession";
import type { MessageKey, MessageParams } from "../i18n/index";

/**
 * agent 侧两条用户可见动作：从某条消息 fork 出新会话，以及跑 agent 生命周期
 * 命令（安装 / 更新）。
 * 从 App.tsx 抽出（2026-10-08）。fork 会连续动 sessionCache / loadedSession /
 * sessions / selected 四张表再拉一次列表，是 App 里最长的单个回调；生命周期命令
 * 则要先把绑定的会话全部解绑再以 drawer 模式发一条 command。两者共享
 * currentRootIdRef 与 setInteractionMode，且都是「先清场再发消息」同一个形状。
 */
export type UseAppAgentActionsParams = {
  boundSessionByRootRef: React.RefObject<Record<string, string | null>>;
  bumpCacheVersion: () => void;
  currentRootIdRef: React.RefObject<string | null>;
  currentSessionRef: React.RefObject<SessionItem | null>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  handleSelectSession: (session: any, options?: { preserveTaskSelection?: boolean; preserveMainView?: boolean }) => Promise<void>;
  handleSendMessage: (message: string, mode: SessionMode, agent: string, model?: string, agentMode?: string, effort?: string, fastService?: "" | "on" | "off", shell?: string, newSessionWorktree?: { create: boolean; branchMode: "new" | "existing"; branch: string }) => Promise<void>;
  loadSessionsForRoot: (rootID: string, options?: { beforeTime?: string; afterTime?: string; replace?: boolean; force?: boolean }) => Promise<void>;
  loadedSessionRef: React.RefObject<Record<string, boolean>>;
  rootSessionKey: (rootId: string, sessionKey: string) => string;
  scopedRootKey: (rootId: string) => string;
  selectedSessionByRootRef: React.RefObject<Record<string, string | null>>;
  selectedSessionRef: React.RefObject<SessionItem | null>;
  sessionCacheRef: React.RefObject<Record<string, Session>>;
  setBoundSessionForRoot: (rootID: string | null | undefined, key: string | null) => void;
  setDrawerOpenForRoot: (rootID: string | null | undefined, open: boolean) => void;
  setDrawerSessionForRoot: (rootID: string | null | undefined, session: Session | SessionItem | null) => void;
  setInteractionMode: React.Dispatch<React.SetStateAction<"main" | "drawer">>;
  setSelectedSession: React.Dispatch<React.SetStateAction<SessionItem | null>>;
  setSessions: React.Dispatch<React.SetStateAction<SessionItem[]>>;
  suppressedAutoBindSessionByRootRef: React.RefObject<Record<string, string | null>>;
  t: (key: MessageKey, params?: MessageParams) => string;
};

/**
 * 两个函数都必须放在 useSendMessage 之后（handleSendMessage 是入参）。
 * 本模块不含 refreshSessionsAfterExternalImport——它在第 1880 行就被 onImported 用掉，
 * 早于这里能给出的位置，所以留在 App 里。这不是漏抽，是位置约束。
 */
export function useAppAgentActions(params: UseAppAgentActionsParams) {
  const { boundSessionByRootRef, bumpCacheVersion, currentRootIdRef, currentSessionRef, getNodeIdForRoot, handleSelectSession, handleSendMessage, loadSessionsForRoot, loadedSessionRef, rootSessionKey, scopedRootKey, selectedSessionByRootRef, selectedSessionRef, sessionCacheRef, setBoundSessionForRoot, setDrawerOpenForRoot, setDrawerSessionForRoot, setInteractionMode, setSelectedSession, setSessions, suppressedAutoBindSessionByRootRef, t } = params;

  const handleForkAgentMessage = useCallback(
    async (
      rootID: string | null | undefined,
      sessionKey: string | null | undefined,
      seq: number,
    ) => {
      const resolvedRoot = String(rootID || currentRootIdRef.current || "").trim();
      const resolvedKey = String(sessionKey || "").trim();
      if (!resolvedRoot || !resolvedKey || seq <= 0) {
        reportError("session.sync_failed", t("session.forkMissing"));
        return;
      }
      let result: Awaited<ReturnType<typeof sessionService.forkSession>>;
      try {
        result = await sessionService.forkSession(resolvedRoot, resolvedKey, seq, getNodeIdForRoot(resolvedRoot));
      } catch (error) {
        const detail = error instanceof Error ? error.message : String(error);
        reportError("session.sync_failed", `${t("session.forkFailed")}: ${detail}`);
        return;
      }
      const forked = result?.session;
      const forkedKey = String(result?.session_key || forked?.key || "").trim();
      if (!forkedKey) {
        reportError("session.sync_failed", t("session.forkFailed"));
        return;
      }
      if (forked) {
        const normalized = {
          ...(forked as any),
          key: forkedKey,
          session_key: forkedKey,
          root_id: resolvedRoot,
        } as Session;
        const cacheKey = rootSessionKey(resolvedRoot, forkedKey);
        sessionCacheRef.current[cacheKey] = normalized;
        loadedSessionRef.current[cacheKey] = true;
        const item = toSessionItem(resolvedRoot, normalized);
        if (item) {
          setSessions((prev) => mergeSessionItems(prev, [item]));
          await handleSelectSession(item);
        } else {
          await handleSelectSession({ key: forkedKey, session_key: forkedKey, root_id: resolvedRoot });
        }
      } else {
        await handleSelectSession({ key: forkedKey, session_key: forkedKey, root_id: resolvedRoot });
      }
      void loadSessionsForRoot(resolvedRoot, { replace: true, force: true });
      bumpCacheVersion();
    },
    [
      bumpCacheVersion,
      handleSelectSession,
      loadSessionsForRoot,
      mergeSessionItems,
      rootSessionKey,
    ],
  );

  const handleRunAgentLifecycleCommand = useCallback(
    async (agentName: string, action: "install" | "update", commands: string[]) => {
      const activeRoot = currentRootIdRef.current;
      if (!activeRoot) {
        throw new Error(t("root.noProjectForCommand"));
      }
      const script = commands.map((command) => command.trim()).filter(Boolean).join("\n");
      if (!script) {
        throw new Error(t("agentConfig.noCommand"));
      }
      const previousBoundKey = boundSessionByRootRef.current[scopedRootKey(activeRoot)];
      if (previousBoundKey && !previousBoundKey.startsWith("pending-")) {
        suppressedAutoBindSessionByRootRef.current[scopedRootKey(activeRoot)] = previousBoundKey;
      }
      selectedSessionRef.current = null;
      currentSessionRef.current = null;
      setSelectedSession(null);
      selectedSessionByRootRef.current[scopedRootKey(activeRoot)] = null;
      setBoundSessionForRoot(activeRoot, null);
      setDrawerSessionForRoot(activeRoot, null);
      setInteractionMode("drawer");
      setDrawerOpenForRoot(activeRoot, true);
      await handleSendMessage(script, "command", "");
      console.info("[agent/lifecycle] command dispatched", {
        agent: agentName,
        action,
        commandCount: commands.length,
      });
    },
    [
      handleSendMessage,
      setBoundSessionForRoot,
      setDrawerOpenForRoot,
      setDrawerSessionForRoot,
      t,
    ],
  );

  return {
    handleForkAgentMessage,
    handleRunAgentLifecycleCommand,
  };
}
