import { useEffect, useMemo, useRef } from "react";
import type { Dispatch, RefObject, SetStateAction } from "react";
import type { I18nContextValue } from "../i18n";
import type { GitHubImportState, ProjectAddMode } from "../components/ProjectAddPopover";
import type { FileEntry } from "../services/directorySort";
import type { FilePayload } from "../services/file";
import type { GitDiffPayload, GitStatusPayload } from "../services/git";
import type { QueuedUserMessage, RelatedFile, RelatedWorktree, Session, TokenUsage } from "../services/session";
import type { TaskDetail } from "../services/tasks";
import type { UpdateState } from "../services/update";
import type { ManagedRootPayload } from "./appMisc";
import type { MultiProjectSessionGroup, PendingSend, SessionItem, SlashCommandResult, WSStatus } from "./appSession";
import type { SessionRuntimeMeta } from "./useSessionStreamCache";
import { buildGitDiffCacheSignature, fetchGitDiff } from "../services/git";
import { invalidateFileCache } from "../services/file";
import { reportError } from "../services/error";
import { setRootNodeMap } from "../services/rootNode";
import { applyNodesFromServer, syncNodesFromServer } from "../services/nodeRegistry";
import { sessionService, setCachedSessionRelatedFiles } from "../services/session";
import { mapManagedRootsToEntries, normalizeUpdateState } from "./appMisc";
import { rootNodeKey } from "./appPath";
import { hasSessionExchanges, normalizeMode, toSessionItem } from "./appSession";
import { normalizeFastService } from "./appTask";
import type { Exchange, SessionQueueItem } from "./appSession";
import type { KanbanTask } from "../services/tasks";

/**
 * WS 事件处理器集合。2026-09 从 App.tsx 整块搬出：25 个 handler + 13 个内部 helper
 * （另有 handleSessionStream 内部的 9 个 case，合计 34 个事件分支）。
 *
 * 参数按用途分四组，组内一律「同名简写」传入——搬过来的 1600 行代码因此一个标识符都不用改。
 * 类型全部由 tsc 从 App 侧实参反推（不是手写、也不是 any），上游改名会立刻编不过。
 */
export type RealtimeEventsContext = {
  /** App 的 ref 池 */
  refs: {
    actionHandlersRef: RefObject<{ open: (params: any) => Promise<void>; open_dir: (params: any) => Promise<void>; }>;
    boundSessionByRootRef: RefObject<Record<string, string | null>>;
    cancelRequestedBySessionRef: RefObject<Record<string, boolean>>;
    currentRootIdRef: RefObject<string | null>;
    currentRootNodeIdRef: RefObject<string | null>;
    currentSessionRef: RefObject<SessionItem | null>;
    drawerSessionByRootRef: RefObject<Record<string, SessionItem | null>>;
    fileRef: RefObject<FilePayload | null>;
    invalidTreeCacheKeysRef: RefObject<Set<string>>;
    loadedSessionRef: RefObject<Record<string, boolean>>;
    managedRootByIdRef: RefObject<Record<string, ManagedRootPayload>>;
    managedRootByKeyRef: RefObject<Record<string, ManagedRootPayload>>;
    managedRootIdsRef: RefObject<Set<string>>;
    optimisticDequeuedIdsRef: RefObject<Record<string, Set<string>>>;
    pendingBySessionRef: RefObject<Record<string, PendingSend>>;
    pendingDraftRef: RefObject<PendingSend | null>;
    pendingRequestRef: RefObject<Record<string, PendingSend>>;
    queueFrozenBySessionRef: RefObject<Record<string, boolean>>;
    queuedMessagesBySessionRef: RefObject<Record<string, QueuedUserMessage[]>>;
    selectedDirRef: RefObject<string | null>;
    selectedSessionByRootRef: RefObject<Record<string, string | null>>;
    selectedSessionRef: RefObject<SessionItem | null>;
    sessionCacheRef: RefObject<Record<string, Session>>;
    sessionListReloadTimerRef: RefObject<number | null>;
    sessionsRef: RefObject<SessionItem[]>;
    suppressedAutoBindSessionByRootRef: RefObject<Record<string, string | null>>;
    taskDetailsByIdRef: RefObject<Record<string, TaskDetail>>;
  };
  /** App 的 setState */
  setters: {
    setAgentsVersion: Dispatch<SetStateAction<number>>;
    setBoundSessionForRoot: (rootID: string | null | undefined, key: string | null) => void;
    setCodexRateLimitsRefreshToken: Dispatch<SetStateAction<number>>;
    setDrawerSessionForRoot: (rootID: string | null | undefined, session: Session | SessionItem | null) => void;
    setGitDiff: Dispatch<SetStateAction<GitDiffPayload | null>>;
    setGitHubImportState: Dispatch<SetStateAction<GitHubImportState>>;
    setMultiProjectSessionGroups: Dispatch<SetStateAction<MultiProjectSessionGroup[]>>;
    setMultiProjectSessionPending: (rootID: string | null | undefined, sessionKey: string | null | undefined, pending: boolean) => void;
    setProjectAddMode: Dispatch<SetStateAction<ProjectAddMode | null>>;
    setQueueVersion: Dispatch<SetStateAction<number>>;
    setRootEntries: Dispatch<SetStateAction<FileEntry[]>>;
    setSelectedPendingByKey: (sessionKey: string, pending: boolean) => void;
    setSelectedSession: Dispatch<SetStateAction<SessionItem | null>>;
    setSessions: Dispatch<SetStateAction<SessionItem[]>>;
    setSlashCommandResults: Dispatch<SetStateAction<Record<string, SlashCommandResult>>>;
    setStatus: Dispatch<SetStateAction<WSStatus>>;
    setUpdateState: Dispatch<SetStateAction<UpdateState>>;
  };
  /** App 的回调与动作 */
  actions: {
    appendAgentChunkForSession: (rootID: string, sessionKey: string, content: string, runtimeHint?: SessionRuntimeMeta) => void;
    appendCompactNoticeForSession: (rootID: string, sessionKey: string, compactNotice: any) => void;
    appendPlanUpdateForSession: (rootID: string, sessionKey: string, planUpdate: any) => void;
    appendThoughtChunkForSession: (rootID: string, sessionKey: string, content: string, thoughtID?: string) => void;
    appendTodoUpdateForSession: (rootID: string, sessionKey: string, todoUpdate: any) => void;
    appendToolCallForSession: (rootID: string, sessionKey: string, toolCall: any, update: boolean) => void;
    applyManagedRootRename: (oldRootID: string, rootPayload: ManagedRootPayload | null | undefined) => boolean;
    applyTaskDetails: (rootId: string, details: TaskDetail[], persist?: boolean) => void;
    attachContextWindowToLatestAssistant: (rootID: string, sessionKey: string, contextWindow?: { totalTokens?: number; modelContextWindow?: number; }, tokenUsage?: TokenUsage) => void;
    bumpCacheVersion: () => void;
    clearSessionStale: (rootID: string | null | undefined, sessionKey: string | null | undefined) => void;
    getNodeIdForRoot: (rootId: string) => string | undefined;
    invalidatePluginsForRoot: (rootId: string) => void;
    loadManagedRootPayloads: (opts?: { force?: boolean; }) => Promise<ManagedRootPayload[] | null>;
    loadMultiProjectSessionGroups: () => Promise<void>;
    loadSessionsForRoot: (rootID: string, options?: { beforeTime?: string; afterTime?: string; replace?: boolean; force?: boolean; }) => Promise<void>;
    markSessionPending: (rootID: string, sessionKey: string) => void;
    markSessionStale: (rootID: string | null | undefined, sessionKey: string | null | undefined) => void;
    playCompletionSound: () => void;
    promotePendingSessionForRoot: (rootID: string, tempKey: string | undefined, sessionKey: string, fallback?: Session | null) => void;
    refreshCurrentFileContent: (rootID: string, changedPath: string) => Promise<void>;
    refreshGitStatus: (rootID: string) => Promise<GitStatusPayload | null>;
    refreshManagedRoots: () => Promise<void>;
    refreshMultiProjectReplyingSessions: () => Promise<void>;
    refreshTaskWorktree: (rootId: string, worktreePath: string, force?: boolean) => Promise<void>;
    refreshTasksForRelatedSession: (root: string, sessionKey: string) => void;
    refreshTreeDir: (rootID: string, dirPath: string, syncMain: boolean) => Promise<void>;
    resolveRootForSessionKey: (sessionKey: string) => string | null;
    restoreActiveSession: (rootID: string | null | undefined, sessionKey: string | null | undefined) => Promise<Session | null>;
    scheduleSessionListReload: (rootID: string, options?: { beforeTime?: string; afterTime?: string; replace?: boolean; force?: boolean; }) => void;
    updateSessionAgentForKey: (rootID: string, sessionKey: string, agent: string, model?: string, agentMode?: string, effort?: string, fastService?: "" | "on" | "off", planMode?: boolean, shell?: string) => void;
    updateSessionRelatedFilesForKey: (rootID: string, sessionKey: string, relatedFiles: RelatedFile[]) => void;
    updateSessionRelatedWorktreeForKey: (rootID: string, sessionKey: string, relatedWorktree: RelatedWorktree | null | undefined) => void;
  };
  /** 稳定值 */
  values: {
    currentRootId: string | null;
    gitDiff: GitDiffPayload | null;
    handleImportedSessionConfirmed: (agentSessionID: string, agentName: string) => void;
    multiProjectSessionsEnabled: boolean;
    rootSessionKey: (rootId: string, sessionKey: string) => string;
    scopedRootKey: (rootId: string) => string;
    t: I18nContextValue["t"];
    treeCacheKey: (rootID: string, dirPath: string) => string;
  };
};

export function useRealtimeEvents(ctx: RealtimeEventsContext) {
  const {
    refs: {
      actionHandlersRef,
      boundSessionByRootRef,
      cancelRequestedBySessionRef,
      currentRootIdRef,
      currentRootNodeIdRef,
      currentSessionRef,
      drawerSessionByRootRef,
      fileRef,
      invalidTreeCacheKeysRef,
      loadedSessionRef,
      managedRootByIdRef,
      managedRootByKeyRef,
      managedRootIdsRef,
      optimisticDequeuedIdsRef,
      pendingBySessionRef,
      pendingDraftRef,
      pendingRequestRef,
      queueFrozenBySessionRef,
      queuedMessagesBySessionRef,
      selectedDirRef,
      selectedSessionByRootRef,
      selectedSessionRef,
      sessionCacheRef,
      sessionListReloadTimerRef,
      sessionsRef,
      suppressedAutoBindSessionByRootRef,
      taskDetailsByIdRef,
    },
    setters: {
      setAgentsVersion,
      setBoundSessionForRoot,
      setCodexRateLimitsRefreshToken,
      setDrawerSessionForRoot,
      setGitDiff,
      setGitHubImportState,
      setMultiProjectSessionGroups,
      setMultiProjectSessionPending,
      setProjectAddMode,
      setQueueVersion,
      setRootEntries,
      setSelectedPendingByKey,
      setSelectedSession,
      setSessions,
      setSlashCommandResults,
      setStatus,
      setUpdateState,
    },
    actions: {
      appendAgentChunkForSession,
      appendCompactNoticeForSession,
      appendPlanUpdateForSession,
      appendThoughtChunkForSession,
      appendTodoUpdateForSession,
      appendToolCallForSession,
      applyManagedRootRename,
      applyTaskDetails,
      attachContextWindowToLatestAssistant,
      bumpCacheVersion,
      clearSessionStale,
      getNodeIdForRoot,
      invalidatePluginsForRoot,
      loadManagedRootPayloads,
      loadMultiProjectSessionGroups,
      loadSessionsForRoot,
      markSessionPending,
      markSessionStale,
      playCompletionSound,
      promotePendingSessionForRoot,
      refreshCurrentFileContent,
      refreshGitStatus,
      refreshManagedRoots,
      refreshMultiProjectReplyingSessions,
      refreshTaskWorktree,
      refreshTasksForRelatedSession,
      refreshTreeDir,
      resolveRootForSessionKey,
      restoreActiveSession,
      scheduleSessionListReload,
      updateSessionAgentForKey,
      updateSessionRelatedFilesForKey,
      updateSessionRelatedWorktreeForKey,
    },
    values: {
      currentRootId,
      gitDiff,
      handleImportedSessionConfirmed,
      multiProjectSessionsEnabled,
      rootSessionKey,
      scopedRootKey,
      t,
      treeCacheKey,
    },
  } = ctx;

  // 2026-09 App.tsx 拆分：34 个 WS 事件处理器整体搬进 useMemo，订阅器只剩「派发」这一件事。
  // 事件回调全同步、无 await，且没有「值变了才动作」的守卫比较，故经 ref 读最新闭包不改变行为。
  const cancelledRef = useRef(false);
  const wsHandlers = useMemo(() => {
    const reloadSessionForReplay = async (
      rootID: string,
      sessionKey: string,
    ) => {
      if (!rootID || !sessionKey) return;
      const restored = await restoreActiveSession(rootID, sessionKey);
      if (cancelledRef.current) return;
      if (!restored) return;
      const cacheKey = rootSessionKey(rootID, sessionKey);
      loadedSessionRef.current[cacheKey] = true;
      clearSessionStale(rootID, sessionKey);
      if (
        (selectedSessionRef.current?.key ||
          selectedSessionRef.current?.session_key) === sessionKey
      ) {
        setSelectedSession((prev) =>
          prev
            ? toSessionItem(rootID, {
                ...(prev as any),
                ...(restored as any),
              })
            : prev,
        );
      }
      if (boundSessionByRootRef.current[scopedRootKey(rootID)] === sessionKey) {
        setDrawerSessionForRoot(rootID, restored);
      }
    };
    const getReplayTargetsForRoot = (rootID: string): string[] => {
      if (!rootID) return [];
      const keys = new Set<string>();
      const rememberedSelectedKey =
        selectedSessionByRootRef.current[scopedRootKey(rootID)] || "";
      if (
        rememberedSelectedKey &&
        !rememberedSelectedKey.startsWith("pending-")
      ) {
        keys.add(rememberedSelectedKey);
      }
      const boundKey = boundSessionByRootRef.current[scopedRootKey(rootID)] || "";
      if (boundKey && !boundKey.startsWith("pending-")) {
        keys.add(boundKey);
      }
      const drawerKey = drawerSessionByRootRef.current[scopedRootKey(rootID)]?.key || "";
      if (drawerKey && !drawerKey.startsWith("pending-")) {
        keys.add(drawerKey);
      }
      const selected = selectedSessionRef.current;
      const selectedKey = selected?.key || selected?.session_key || "";
      const selectedRoot =
        (selected?.root_id as string | undefined) || currentRootIdRef.current;
      if (
        selectedRoot === rootID &&
        selectedKey &&
        !selectedKey.startsWith("pending-")
      ) {
        keys.add(selectedKey);
      }
      return Array.from(keys);
    };
    const replayTargetsForAllRoots = () => {
      const replayCacheKeys = new Set<string>();
      for (const rootID of managedRootIdsRef.current) {
        if (!rootID) continue;
        const replayTargets = getReplayTargetsForRoot(rootID);
        for (const sessionKey of replayTargets) {
          replayCacheKeys.add(rootSessionKey(rootID, sessionKey));
          void reloadSessionForReplay(rootID, sessionKey);
        }
      }
      for (const cacheKey of Object.keys(sessionCacheRef.current)) {
        if (replayCacheKeys.has(cacheKey)) {
          continue;
        }
        const separator = cacheKey.indexOf("::");
        if (separator <= 0) {
          continue;
        }
        const rootID = cacheKey.slice(0, separator);
        const sessionKey = cacheKey.slice(separator + 2);
        if (!rootID || !sessionKey || !hasSessionExchanges(sessionCacheRef.current[cacheKey])) {
          continue;
        }
        markSessionStale(rootID, sessionKey);
      }
    };
    const refreshSessionRelatedFiles = async (
      rootID: string,
      sessionKey: string,
    ) => {
      if (!rootID || !sessionKey) return;
      const relatedFiles = await sessionService.getSessionRelatedFiles(
        rootID,
        sessionKey,
        getNodeIdForRoot(rootID),
      );
      if (cancelledRef.current) return;
      await setCachedSessionRelatedFiles(rootID, sessionKey, relatedFiles, getNodeIdForRoot(rootID));
      updateSessionRelatedFilesForKey(rootID, sessionKey, relatedFiles);
    };
    const handleSessionStreamDone = (rootID: string, sessionKey: string) => {
      const cacheKey = rootSessionKey(rootID, sessionKey);
      const wasCanceled = !!cancelRequestedBySessionRef.current[cacheKey];
      if (wasCanceled) {
        delete cancelRequestedBySessionRef.current[cacheKey];
      }
      delete pendingBySessionRef.current[cacheKey];
      const clearPendingAck = <T,>(session: T): T => {
        const exchanges = (session as any)?.exchanges;
        if (!wasCanceled || !Array.isArray(exchanges)) {
          return session;
        }
        return {
          ...(session as any),
          exchanges: exchanges.map((exchange: any) =>
            exchange?.pending_ack === true
              ? { ...exchange, pending_ack: false }
              : exchange,
          ),
        } as T;
      };
      const queued = queuedMessagesBySessionRef.current[cacheKey] || [];
      const queueFrozen = !!queueFrozenBySessionRef.current[cacheKey];
      const hiddenQueued = optimisticDequeuedIdsRef.current[cacheKey];
      const hasQueuedContinuation =
        (queued.length > 0 && !queueFrozen) ||
        !!(hiddenQueued && hiddenQueued.size > 0);
      if (hasQueuedContinuation && !wasCanceled) {
        markSessionPending(rootID, sessionKey);
        return;
      }
      const cached = sessionCacheRef.current[cacheKey];
      if (cached && cached.key === sessionKey) {
        sessionCacheRef.current[cacheKey] = clearPendingAck({
          ...(cached as any),
          pending: false,
        } as Session);
      }
      setSelectedPendingByKey(sessionKey, false);
      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        const prevRoot =
          (prev?.root_id as string | undefined) || currentRootIdRef.current;
        if (
          !prev ||
          prevKey !== sessionKey ||
          prevRoot !== rootID ||
          !(prev as any).pending
        ) {
          return prev;
        }
        return clearPendingAck({
          ...(prev as any),
          pending: false,
        } as SessionItem);
      });
      const drawer = drawerSessionByRootRef.current[scopedRootKey(rootID)];
      if (drawer && drawer.key === sessionKey) {
        const latest = wasCanceled
          ? sessionCacheRef.current[cacheKey] || drawer
          : drawer;
        setDrawerSessionForRoot(rootID, clearPendingAck({
          ...(latest as any),
          pending: false,
        } as Session));
      }
      if (currentRootIdRef.current === rootID) {
        setSessions((prev) =>
          prev.map((item) => {
            const itemKey = item.key || item.session_key;
            if (itemKey !== sessionKey) {
              return item;
            }
            return clearPendingAck({
              ...(item as any),
              pending: false,
            } as SessionItem);
          }),
        );
      }
      setMultiProjectSessionPending(rootID, sessionKey, false);
      bumpCacheVersion();
    };

    const handleSessionStream = (payload: any) => {
      const wsNid = String((payload as any)?._nodeId || (payload as any)?.nodeId || "").trim();
      const curNid = String(currentRootNodeIdRef.current || "").trim();
      if (wsNid && curNid && wsNid !== curNid) { return; }
      const streamKey =
        typeof payload?.session_key === "string" ? payload.session_key : "";
      const activeRoot =
        typeof payload?.root_id === "string" && payload.root_id
          ? payload.root_id
          : resolveRootForSessionKey(streamKey) || currentRootIdRef.current;
      if (!streamKey || !activeRoot) return;
      const ck = rootSessionKey(activeRoot, streamKey);
      let pending = pendingBySessionRef.current[ck];
      if (!pending) {
        const draft = pendingDraftRef.current;
        if (
          draft &&
          draft.rootId === activeRoot &&
          streamKey !==
            (suppressedAutoBindSessionByRootRef.current[scopedRootKey(activeRoot)] || "")
        ) {
          pending = draft;
          pendingBySessionRef.current[ck] = draft;
          pendingDraftRef.current = null;
          console.info("[session/stream] attach_pending_draft", { rootId: activeRoot, streamKey, requestId: draft.requestId, tempKey: draft.tempKey || null });
        }
      }
      const boundKey = boundSessionByRootRef.current[scopedRootKey(activeRoot)] || "";
      const suppressedAutoBindKey =
        suppressedAutoBindSessionByRootRef.current[scopedRootKey(activeRoot)] || "";
      if (
        streamKey !== suppressedAutoBindKey &&
        (!boundKey ||
          (typeof boundKey === "string" && boundKey.startsWith("pending-")))
      ) {
        if (suppressedAutoBindKey && streamKey !== suppressedAutoBindKey) {
          suppressedAutoBindSessionByRootRef.current[scopedRootKey(activeRoot)] = null;
        }
        setBoundSessionForRoot(activeRoot, streamKey);
        if (pending) {
          const pendingName =
            (drawerSessionByRootRef.current[scopedRootKey(activeRoot)]?.key ===
            pending.tempKey &&
            typeof (drawerSessionByRootRef.current[scopedRootKey(activeRoot)] as any)?.name ===
              "string"
              ? ((drawerSessionByRootRef.current[scopedRootKey(activeRoot)] as any).name as string)
              : "") ||
            ((selectedSessionRef.current?.key ||
              selectedSessionRef.current?.session_key) === pending.tempKey &&
            (((selectedSessionRef.current?.root_id as string | undefined) ||
              currentRootIdRef.current) === activeRoot) &&
            typeof selectedSessionRef.current?.name === "string"
              ? selectedSessionRef.current.name
              : "") ||
            t("session.new");
          const userEx = {
            role: "user",
            content: pending.message,
            timestamp: pending.timestamp,
            model: pending.model,
              mode: pending.agentMode,
              effort: pending.effort,
              fast_service: pending.fastService || "",
              shell: pending.shell || "",
          };
          const cached =
            sessionCacheRef.current[ck] ||
            ({
              key: streamKey,
              type: pending.mode,
              agent: pending.agent,
              model: pending.model,
                mode: pending.agentMode,
                effort: pending.effort,
                fast_service: pending.fastService || "",
                shell: pending.shell || "",
              name: pendingName,
              created_at: pending.timestamp,
              updated_at: pending.timestamp,
              exchanges: [],
            } as any);
          const prevExchanges = Array.isArray((cached as any).exchanges)
            ? ((cached as any).exchanges as Exchange[])
            : [];
          sessionCacheRef.current[ck] = {
            ...(cached as any),
            exchanges: prevExchanges.length > 0 ? prevExchanges : [userEx],
            updated_at: new Date().toISOString(),
          } as Session;
          bumpCacheVersion();
        }
        const seeded = sessionCacheRef.current[ck];
        if (seeded) {
          setDrawerSessionForRoot(activeRoot, {
            ...(seeded as any),
            pending: true,
          } as Session);
        }
      }
      if (pending?.tempKey) {
        console.info("[session/stream] promote_pending", { rootId: activeRoot, tempKey: pending.tempKey, streamKey, requestId: pending.requestId });
        promotePendingSessionForRoot(
          activeRoot,
          pending.tempKey,
          streamKey,
          sessionCacheRef.current[ck] || null,
        );
      }
      const event = payload.event;
      if (!event?.type) return;
      // message_chunk/thought_chunk 是最高频事件：首次进 pending 用幂等
      // markSessionPending（内部已在 pending 时只更新 ref 不 setState），
      // 缓存版本 bump 走 30ms debounce —— 100 token/s 时渲染次数从 100/s 降到 ~33/s。
      const markStreamPending = () => {
        if (event.type === "message_done" || event.type === "error") return;
        markSessionPending(activeRoot, streamKey);
      };
      markStreamPending();
      const isStreamingChunk =
        event.type === "message_chunk" || event.type === "thought_chunk";
      if (!isStreamingChunk) {
        bumpCacheVersion();
      }
      switch (event.type) {
        case "message_chunk":
          appendAgentChunkForSession(
            activeRoot,
            streamKey,
            event.data?.content || "",
            pending
              ? {
                  agent: pending.agent,
                  model: pending.model,
                    mode: pending.agentMode,
                    effort: pending.effort,
                    fast_service: pending.fastService || "",
                  }
              : undefined,
          );
          break;
        case "thought_chunk":
          appendThoughtChunkForSession(
            activeRoot,
            streamKey,
            event.data?.content || "",
            event.data?.id || "",
          );
          break;
        case "tool_call":
          appendToolCallForSession(
            activeRoot,
            streamKey,
            event.data || {},
            false,
          );
          break;
        case "tool_call_update":
          appendToolCallForSession(
            activeRoot,
            streamKey,
            event.data || {},
            true,
          );
          break;
        case "todo_update":
          appendTodoUpdateForSession(
            activeRoot,
            streamKey,
            event.data || {},
          );
          break;
        case "plan_update":
          appendPlanUpdateForSession(
            activeRoot,
            streamKey,
            event.data || {},
          );
          break;
        case "compact_notice":
          appendCompactNoticeForSession(
            activeRoot,
            streamKey,
            event.data || {},
          );
          break;
        case "message_done":
          attachContextWindowToLatestAssistant(
            activeRoot,
            streamKey,
            event.data?.contextWindow,
            event.data?.tokenUsage,
          );
          setCodexRateLimitsRefreshToken((value) => value + 1);
          break;
        case "error":
          reportError(
            "session.resume_failed",
            event.data?.message || t("session.resumeFailed"),
            {
              details: {
                rootId: activeRoot,
                sessionKey: streamKey,
                eventType: event.type,
              },
            },
          );
          handleSessionStreamDone(activeRoot, streamKey);
          break;
      }
    };
    const handleSlashCommandStream = (payload: any) => {
      const wsNid2 = String((payload as any)?._nodeId || (payload as any)?.nodeId || "").trim();
      const curNid2 = String(currentRootNodeIdRef.current || "").trim();
      if (wsNid2 && curNid2 && wsNid2 !== curNid2) { return; }
      const sessionKey =
        typeof payload?.session_key === "string" ? payload.session_key : "";
      const rootID =
        typeof payload?.root_id === "string" && payload.root_id
          ? payload.root_id
          : resolveRootForSessionKey(sessionKey) || currentRootIdRef.current;
      const command =
        typeof payload?.command === "string" ? payload.command : "status";
      const requestId =
        typeof payload?.request_id === "string"
          ? payload.request_id
          : typeof payload?.id === "string"
            ? payload.id
            : "";
      const event = payload?.event;
      if (!rootID || !sessionKey || !event?.type) {
        return;
      }
      const resultKey = rootSessionKey(rootID, sessionKey);
      if (event.type === "message_chunk") {
        const chunk = typeof event.data?.content === "string" ? event.data.content : "";
        if (!chunk) {
          return;
        }
        setSlashCommandResults((prev) => {
          const current = prev[resultKey];
          if (!current && sessionKey.startsWith("transient-")) {
            return prev;
          }
          if (
            current?.requestId &&
            requestId &&
            current.requestId !== requestId
          ) {
            return prev;
          }
          return {
            ...prev,
            [resultKey]: {
              rootId: rootID,
              sessionKey,
              requestId: current?.requestId || requestId,
              command: current?.command || command,
              content: `${current?.content || ""}${chunk}`,
              status: "running",
            },
          };
        });
        return;
      }
      if (event.type === "login_notice") {
        const notice = event.data || {};
        const noticeStatus = typeof notice.status === "string" ? notice.status : "";
        const failed = noticeStatus === "error";
        const complete = noticeStatus === "success";
        setSlashCommandResults((prev) => {
          const current = prev[resultKey];
          if (!current && sessionKey.startsWith("transient-")) {
            return prev;
          }
          if (
            current?.requestId &&
            requestId &&
            current.requestId !== requestId
          ) {
            return prev;
          }
          return {
            ...prev,
            [resultKey]: {
              rootId: rootID,
              sessionKey,
              requestId: current?.requestId || requestId,
              command: current?.command || command || "login",
              content: current?.content || "",
              status: failed ? "failed" : complete ? "complete" : "running",
              error:
                failed && typeof notice.error === "string"
                  ? notice.error
                  : current?.error,
              createdAt: current?.createdAt || Date.now(),
              loginNotice: {
                ...current?.loginNotice,
                status: noticeStatus,
                loginId: typeof notice.loginId === "string" ? notice.loginId : current?.loginNotice?.loginId,
                verificationUrl:
                  typeof notice.verificationUrl === "string"
                    ? notice.verificationUrl
                    : current?.loginNotice?.verificationUrl,
                userCode:
                  typeof notice.userCode === "string"
                    ? notice.userCode
                    : current?.loginNotice?.userCode,
                error: typeof notice.error === "string" ? notice.error : current?.loginNotice?.error,
                authMode:
                  typeof notice.authMode === "string"
                    ? notice.authMode
                    : current?.loginNotice?.authMode,
                planType:
                  typeof notice.planType === "string"
                    ? notice.planType
                    : current?.loginNotice?.planType,
              },
            },
          };
        });
        if (failed) {
          reportError("session.slash_command_failed", notice.error || t("session.loginFailed"), {
            details: { rootId: rootID, sessionKey, command },
          });
        }
        return;
      }
      if (event.type === "error") {
        const message =
          typeof event.data?.message === "string"
            ? event.data.message
            : t("session.commandFailed");
        setSlashCommandResults((prev) => {
          const current = prev[resultKey];
          if (!current && sessionKey.startsWith("transient-")) {
            return prev;
          }
          if (
            current?.requestId &&
            requestId &&
            current.requestId !== requestId
          ) {
            return prev;
          }
          return {
            ...prev,
            [resultKey]: {
              rootId: rootID,
              sessionKey,
              requestId: current?.requestId || requestId,
              command: current?.command || command,
              content: current?.content || "",
              status: "failed",
              error: message,
            },
          };
        });
        reportError("session.slash_command_failed", message, {
          details: { rootId: rootID, sessionKey, command },
        });
      }
    };
    const handleSlashCommandDone = (payload: any) => {
      const sessionKey =
        typeof payload?.session_key === "string" ? payload.session_key : "";
      const rootID =
        typeof payload?.root_id === "string" && payload.root_id
          ? payload.root_id
          : resolveRootForSessionKey(sessionKey) || currentRootIdRef.current;
      if (!rootID || !sessionKey) {
        return;
      }
      const resultKey = rootSessionKey(rootID, sessionKey);
      setSlashCommandResults((prev) => {
        const current = prev[resultKey];
        if (!current || current.status === "failed") {
          return prev;
        }
        const requestId =
          typeof payload?.request_id === "string"
            ? payload.request_id
            : typeof payload?.id === "string"
              ? payload.id
              : "";
        if (
          current.requestId &&
          requestId &&
          current.requestId !== requestId
        ) {
          return prev;
        }
        return {
          ...prev,
          [resultKey]: {
            ...current,
            status: "complete",
          },
        };
      });
    };
    const dirname = (path: string): string => {
      const clean = (path || "").replace(/^\/+|\/+$/g, "");
      if (!clean || clean === ".") return ".";
      const idx = clean.lastIndexOf("/");
      return idx <= 0 ? "." : clean.slice(0, idx);
    };
    const currentDirAPI = (rootID: string): string => {
      const selected = selectedDirRef.current;
      if (!selected || selected === rootID) return ".";
      return selected;
    };
    const stringList = (value: unknown): string[] => {
      if (!Array.isArray(value)) return [];
      return Array.from(
        new Set(
          value.filter(
            (item): item is string => typeof item === "string" && item !== "",
          ),
        ),
      );
    };
    const handleFileChangedBatch = (payload: any) => {
      const rootID =
        typeof payload?.root_id === "string" ? payload.root_id : "";
      if (!rootID) return;

      const paths = stringList(payload?.paths);
      const events = Array.isArray(payload?.events) ? payload.events : [];
      const dirPathSet = new Set(stringList(payload?.dirs));
      for (const path of paths) {
        const parentDir = dirname(path);
        dirPathSet.add(parentDir);
        const event = events.find((item: any) => item?.path === path);
        const op = typeof event?.op === "string" ? event.op : "";
        if (
          event?.is_dir === true ||
          op.includes("REMOVE") ||
          op.includes("RENAME")
        ) {
          dirPathSet.add(path);
        }
      }
      const dirs = Array.from(dirPathSet);
      if (paths.length === 0 && dirs.length === 0) return;

      for (const path of paths) {
        invalidateFileCache(rootID, path);
      }
      if (
        [...paths, ...dirs].some((path) => {
          const normalized = String(path || "").replace(/\\/g, "/").replace(/^\/+/, "");
          return normalized === ".mindfs/plugins" || normalized.startsWith(".mindfs/plugins/");
        })
      ) {
        invalidatePluginsForRoot(rootID);
      }

      const currentFile = fileRef.current;
      const currentFileRoot =
        currentFile?.root || currentRootIdRef.current || "";
      if (
        currentFile &&
        currentFileRoot === rootID &&
        paths.includes(currentFile.path)
      ) {
        void refreshCurrentFileContent(rootID, currentFile.path);
      }

      void refreshGitStatus(rootID).then((next) => {
        if (!next?.available) {
          if (rootID === currentRootIdRef.current) {
            setGitDiff(null);
          }
          return;
        }
        const changedDiffPath = gitDiff?.path || "";
        if (
          rootID === currentRootIdRef.current &&
          changedDiffPath &&
          paths.includes(changedDiffPath)
        ) {
          const target = next.items.find(
            (item) => item.path === changedDiffPath,
          );
          if (!target) {
            setGitDiff(null);
            return;
          }
          void fetchGitDiff(rootID, changedDiffPath, {
            cacheSignature: buildGitDiffCacheSignature(target),
          })
            .then(setGitDiff)
            .catch((err) => {
              console.error("[git.diff.refresh] failed", {
                rootID,
                changedPath: changedDiffPath,
                err,
              });
            });
        }
      });

      const currentDir = currentDirAPI(rootID);
      for (const dir of dirs) {
        invalidTreeCacheKeysRef.current.add(treeCacheKey(rootID, dir));
        if (rootID === currentRootIdRef.current && dir === currentDir) {
          void refreshTreeDir(rootID, dir, true);
        }
      }
    };
    const handleFileChanged = (payload: any) => {
      const rootID =
        typeof payload?.root_id === "string" ? payload.root_id : "";
      const changedPath = typeof payload?.path === "string" ? payload.path : "";
      if (!rootID || !changedPath) return;
      const dirs = [dirname(changedPath)];
      if (payload?.is_dir === true) {
        dirs.push(changedPath);
      }
      handleFileChangedBatch({
        root_id: rootID,
        paths: [changedPath],
        dirs,
        events: [
          { path: changedPath, op: payload?.op, is_dir: payload?.is_dir },
        ],
      });
    };
    return {
      "ws.connecting": (event: any, payload: any) => {
          setStatus("connecting");
      },
      "ws.connected": (event: any, payload: any) => {
          setStatus("connected");
          void refreshManagedRoots();
          if (currentRootIdRef.current) {
            const newest = sessionsRef.current[0]?.updated_at || "";
            void scheduleSessionListReload(
              currentRootIdRef.current,
              newest ? { afterTime: newest } : { replace: true },
            );
          }
          if (multiProjectSessionsEnabled) {
            void refreshMultiProjectReplyingSessions();
            void loadMultiProjectSessionGroups();
          }
          replayTargetsForAllRoots();
      },
      "ws.reconnecting": (event: any, payload: any) => {
          setStatus("reconnecting");
      },
      "ws.reconnected": (event: any, payload: any) => {
          setStatus("connected");
          void refreshManagedRoots();
          if (currentRootIdRef.current) {
            const newest = sessionsRef.current[0]?.updated_at || "";
            void scheduleSessionListReload(
              currentRootIdRef.current,
              newest ? { afterTime: newest } : { replace: true },
            );
          }
          if (multiProjectSessionsEnabled) {
            void refreshMultiProjectReplyingSessions();
            void loadMultiProjectSessionGroups();
          }
          replayTargetsForAllRoots();
      },
      "ws.closed": (event: any, payload: any) => {
          setStatus(currentRootIdRef.current ? "reconnecting" : "disconnected");
      },
      "nodes.changed": (event: any, payload: any) => {
          void syncNodesFromServer().then((ns) => { try { applyNodesFromServer(ns as any); } catch {} }).catch(() => {});
          void loadManagedRootPayloads().then(() => {
            if (multiProjectSessionsEnabled) {
              void refreshMultiProjectReplyingSessions();
              void loadMultiProjectSessionGroups();
            }
          }).catch(() => {});
      },
      "root.changed": (event: any, payload: any) => {
          if (
            payload?.action === "renamed" &&
            typeof payload?.old_root_id === "string" &&
            typeof payload?.root_id === "string"
          ) {
            const rootPayload =
              payload?.root && typeof payload.root === "object"
                ? ({ ...(payload.root as ManagedRootPayload), id: payload.root_id } as ManagedRootPayload)
                : null;
            if (!rootPayload) {
              return;
            }
            applyManagedRootRename(payload.old_root_id, rootPayload);
            if (currentRootIdRef.current === payload.old_root_id) {
              void actionHandlersRef.current.open_dir({
                path: payload.root_id,
                root: payload.root_id,
                isRoot: true,
                forceDirectory: true,
                preservePluginQuery: true,
              });
            }
            return;
          }
          if (
            payload?.action === "display_name_changed" &&
            typeof payload?.root_id === "string"
          ) {
            const rootId = String(payload.root_id).trim();
            const rootPayload =
              payload?.root && typeof payload.root === "object"
                ? ({ ...(payload.root as ManagedRootPayload), id: rootId } as ManagedRootPayload)
                : null;
            if (!rootPayload || !rootId) {
              return;
            }
            const merged = { ...(managedRootByIdRef.current[rootId] || {} as ManagedRootPayload), ...rootPayload, id: rootId } as ManagedRootPayload;
            const nextById = { ...managedRootByIdRef.current, [rootId]: merged };
            managedRootByIdRef.current = nextById as Record<string, ManagedRootPayload>;
            const evtNid = String((merged as any)._nodeId || "").trim();
            if (evtNid) managedRootByKeyRef.current[rootNodeKey(evtNid, rootId)] = merged;
            setRootNodeMap(nextById as Record<string, any>);
            const ids = Array.from(managedRootIdsRef.current);
            setRootEntries(mapManagedRootsToEntries(ids.map((id) => nextById[id]).filter(Boolean) as ManagedRootPayload[]));
            setMultiProjectSessionGroups((prev) =>
              prev.map((g) =>
                g.rootId === rootId
                  ? { ...g, rootName: String((merged as any)?.display_name || (merged as any)?.id || g.rootName || rootId) }
                  : g,
              ),
            );
            return;
          }
          void refreshManagedRoots();
      },
      "session.imported": (event: any, payload: any) => {
          const rootID =
            typeof payload?.root_id === "string" ? payload.root_id : "";
          const agentName =
            typeof payload?.agent === "string" ? payload.agent : "";
          const agentSessionID =
            typeof payload?.agent_session_id === "string"
              ? payload.agent_session_id.trim()
              : "";
          if (!rootID) {
            return;
          }
          if (rootID === currentRootIdRef.current) {
            void scheduleSessionListReload(rootID, { replace: true });
            handleImportedSessionConfirmed(agentSessionID, agentName);
          }
          if (multiProjectSessionsEnabled) {
            void loadMultiProjectSessionGroups();
          }
          return;
      },
      "session.created": (event: any, payload: any) => {
          const rootID =
            typeof payload?.root_id === "string" ? payload.root_id : "";
          if (rootID && rootID === currentRootIdRef.current) {
            void scheduleSessionListReload(rootID, { replace: true });
          }
          if (rootID && multiProjectSessionsEnabled) {
            void loadMultiProjectSessionGroups();
          }
          return;
      },
      "session.stream": (event: any, payload: any) => {
          handleSessionStream(payload);
      },
      "session.slash_command.stream": (event: any, payload: any) => {
          handleSlashCommandStream(payload);
      },
      "session.slash_command.done": (event: any, payload: any) => {
          handleSlashCommandDone(payload);
      },
      "session.queue.updated": (event: any, payload: any) => {
          const rootID =
            typeof payload?.root_id === "string" ? payload.root_id : "";
          const sessionKey =
            typeof payload?.session_key === "string" ? payload.session_key : "";
          if (!rootID || !sessionKey) {
            return;
          }
          const incomingQueue = Array.isArray(payload?.queue)
            ? (payload.queue.filter(
                (item: any) =>
                  item &&
                  typeof item.id === "string" &&
                  typeof item.content === "string",
              ) as SessionQueueItem[])
            : [];
          const cacheKey = rootSessionKey(rootID, sessionKey);
          const hidden = optimisticDequeuedIdsRef.current[cacheKey];
          const queue = hidden && hidden.size > 0
            ? incomingQueue.filter((item) => !hidden.has(item.id))
            : incomingQueue;
          queueFrozenBySessionRef.current[cacheKey] = payload?.queue_frozen === true;
          if (hidden) {
            for (const queueId of Array.from(hidden)) {
              if (!incomingQueue.some((item) => item.id === queueId)) {
                hidden.delete(queueId);
              }
            }
            if (hidden.size === 0) {
              delete optimisticDequeuedIdsRef.current[cacheKey];
            }
          }
          queuedMessagesBySessionRef.current[cacheKey] = queue;
          setQueueVersion((v) => v + 1);
          return;
      },
      "session.accepted": (event: any, payload: any) => {
          {
            const wsPayloadNid = String((payload as any)?._nodeId || (payload as any)?.nodeId || "").trim();
            const wsCurNid = String(currentRootNodeIdRef.current || "").trim();
            if (wsPayloadNid && wsCurNid && wsPayloadNid !== wsCurNid) { return; }
          }
          const requestId =
            typeof payload?.request_id === "string" ? payload.request_id : "";
          const pending = pendingRequestRef.current[requestId];
          if (!requestId || !pending) {
            console.warn("[session/ws] accepted_without_pending", { requestId, payloadSessionKey: typeof payload?.session_key === "string" ? payload.session_key : null });
            return;
          }
          console.info("[session/ws] accepted", { requestId, rootId: pending.rootId, sessionKey: pending.sessionKey || null, tempKey: pending.tempKey || null });
          delete pendingRequestRef.current[requestId];
          const acceptedTimestamp =
            typeof payload?.timestamp === "string" &&
            !Number.isNaN(Date.parse(payload.timestamp))
              ? payload.timestamp
              : pending.timestamp;
          const acceptedSessionKey =
            typeof payload?.session_key === "string" ? payload.session_key : "";
          if (!pending.sessionKey && pending.tempKey && acceptedSessionKey) {
            const cacheKey = rootSessionKey(pending.rootId, acceptedSessionKey);
            pendingBySessionRef.current[cacheKey] = {
              ...pending,
              timestamp: acceptedTimestamp,
              sessionKey: acceptedSessionKey,
              model_display_name:
                typeof payload?.model_display_name === "string"
                  ? payload.model_display_name
                  : "",
            };
            setMultiProjectSessionPending(pending.rootId, pending.tempKey, false);
            setMultiProjectSessionPending(pending.rootId, acceptedSessionKey, true);
            if (pendingDraftRef.current?.requestId === pending.requestId) {
              pendingDraftRef.current = null;
            }
            promotePendingSessionForRoot(
              pending.rootId,
              pending.tempKey,
              acceptedSessionKey,
              sessionCacheRef.current[cacheKey] || null,
            );
          }
          const markAccepted = (
            sess: Session | SessionItem | null | undefined,
          ): Session | null => {
            if (!sess) return null;
            // 服务端若在 accepted 里带了 seq（当前版本不带，OnStart 之后才可知），
            // 一并写入，使乐观条目立即成为「已持久化条目」。
            const acceptedSeq = Number((payload as any)?.seq || 0);
            const exchanges = Array.isArray((sess as any).exchanges)
              ? ((sess as any).exchanges as Exchange[]).map((exchange) =>
                  exchange.pending_ack === true &&
                  exchange.content === pending.message &&
                  exchange.timestamp === pending.timestamp
                    ? {
                        ...exchange,
                        timestamp: acceptedTimestamp,
                        pending_ack: false,
                        ...(acceptedSeq > 0 ? { seq: acceptedSeq } : {}),
                        model_display_name:
                          exchange.model_display_name ||
                          (typeof payload?.model_display_name === "string"
                            ? payload.model_display_name
                            : ""),
                      }
                    : exchange,
                )
              : [];
            return {
              ...(sess as any),
              exchanges,
              model_display_name:
                (sess as any).model_display_name ||
                (typeof payload?.model_display_name === "string"
                  ? payload.model_display_name
                  : ""),
              updated_at: acceptedTimestamp,
            } as Session;
          };
          const acceptedTargetKey = pending.sessionKey || acceptedSessionKey;
          if (acceptedTargetKey) {
            const cacheKey = rootSessionKey(pending.rootId, acceptedTargetKey);
            const accepted = markAccepted(sessionCacheRef.current[cacheKey]);
            if (accepted) {
              sessionCacheRef.current[cacheKey] = accepted;
              bumpCacheVersion();
            }
          }
          const latestDrawer = drawerSessionByRootRef.current[scopedRootKey(pending.rootId)];
          const drawerKey = latestDrawer?.key || "";
          if (
            drawerKey &&
            (drawerKey === pending.sessionKey ||
              drawerKey === pending.tempKey ||
              drawerKey === acceptedSessionKey)
          ) {
            const accepted = markAccepted(latestDrawer);
            if (accepted) {
              setDrawerSessionForRoot(pending.rootId, accepted);
            }
          }
          return;
      },
      "session.error": (event: any, payload: any) => {
          {
            const wsPayloadNid = String((payload as any)?._nodeId || (payload as any)?.nodeId || "").trim();
            const wsCurNid = String(currentRootNodeIdRef.current || "").trim();
            if (wsPayloadNid && wsCurNid && wsPayloadNid !== wsCurNid) { return; }
          }
          const requestId =
            typeof payload?.request_id === "string" ? payload.request_id : "";
          const pending = requestId
            ? pendingRequestRef.current[requestId]
            : null;
          if (!requestId || !pending) {
            console.warn("[session/ws] error_without_pending", { requestId, payloadSessionKey: typeof payload?.session_key === "string" ? payload.session_key : null });
            return;
          }
          console.warn("[session/ws] error", { requestId, rootId: pending.rootId, sessionKey: pending.sessionKey || null, tempKey: pending.tempKey || null });
          delete pendingRequestRef.current[requestId];
          const targetKey = pending.tempKey || "";
          const failedKey = pending.sessionKey || targetKey;
          const rootID = pending.rootId;
          if (failedKey) {
            setMultiProjectSessionPending(rootID, failedKey, false);
          }
          const latestDrawer = drawerSessionByRootRef.current[scopedRootKey(rootID)];
          if (targetKey && latestDrawer?.key === targetKey) {
            const exchanges = Array.isArray((latestDrawer as any).exchanges)
              ? ((latestDrawer as any).exchanges as Exchange[]).map(
                  (exchange) =>
                    exchange.pending_ack === true &&
                    exchange.content === pending.message &&
                    exchange.timestamp === pending.timestamp
                      ? { ...exchange, pending_ack: false }
                      : exchange,
                )
              : [];
            setDrawerSessionForRoot(rootID, {
              ...(latestDrawer as any),
              pending: false,
              exchanges,
            } as Session);
          }
          return;
      },
      "session.done": (event: any, payload: any) => {
          const wsPayloadNid = String((payload as any)?._nodeId || (payload as any)?.nodeId || (payload as any)?.session?._nodeId || "").trim();
          const wsCurNid = String(currentRootNodeIdRef.current || "").trim();
          if (wsPayloadNid && wsCurNid && wsPayloadNid !== wsCurNid) { return; }
          const sessionKey =
            typeof payload?.session_key === "string" ? payload.session_key : "";
          const rootID =
            typeof payload?.root_id === "string" && payload.root_id
              ? payload.root_id
              : resolveRootForSessionKey(sessionKey) ||
                currentRootIdRef.current ||
                "";
          if (rootID && sessionKey) {
            if (payload?.replay !== true) {
              playCompletionSound();
            }
            setMultiProjectSessionPending(rootID, sessionKey, false);
            handleSessionStreamDone(rootID, sessionKey);
            // done 后重锚定（仅正在查看/绑定的会话）：restoreActiveSession 以服务端持久化窗口
            // 替换缓存。持久化完成后服务端窗口不含 seq=0（JSONL 在生成结束时写入），restore
            // 的 localTransient 合并不回填，缓存里的瞬时尾巴随之清除——否则窗口化合并（F1）
            // 会把已持久化的轮次以 seq=0 形式重复追加在窗口后面。同时自愈断连间隙丢的 chunk。
            // 队列续轮（仍在流式）跳过，等它自己的 done；非查看中的会话不重载（避免覆盖
            // 其它标签页正在流式写入的同一缓存）。
            // replay=true 的 done 不重载：它是服务端对 session.ready 的一次性回执（「你离线期间
            // 这一轮结束了」），而 restoreActiveSession 自己又会发 session.ready，于是
            // done → restore → ready → done 形成自持闭环（实测空转 18 次/秒、每轮一次 ?latest=20）。
            // 断连恢复不依赖这条路径：ws.reconnected 的 replayTargetsForAllRoots 已经重载过窗口，
            // 且服务端窗口自带 pending 状态。这里只保留「真·回合结束」的 done 触发重锚定。
            if (
              payload?.replay !== true &&
              getReplayTargetsForRoot(rootID).includes(sessionKey) &&
              !sessionService.isSessionStreaming(sessionKey)
            ) {
              void reloadSessionForReplay(rootID, sessionKey);
            }
            // 会话刚结束，服务端 updated_at/context_window 已持久化。
            // afterTime 增量会被"updated_at 严格大于旧 newest"排除刚结束的会话（竞态），
            // 必须 replace 全量重拉才能带上最新 meta。频率低 + 300ms debounce，成本可接受。
            void scheduleSessionListReload(rootID, { replace: true });
            if (multiProjectSessionsEnabled) {
              void loadMultiProjectSessionGroups();
            }
          } else if (currentRootIdRef.current) {
            void scheduleSessionListReload(currentRootIdRef.current, {
              replace: true,
            });
            if (multiProjectSessionsEnabled) {
              void refreshMultiProjectReplyingSessions();
              void loadMultiProjectSessionGroups();
            }
          }
          return;
      },
      "session.user_message": (event: any, payload: any) => {
          const wsPayloadNid2 = String((payload as any)?._nodeId || (payload as any)?.nodeId || (payload as any)?.session?._nodeId || "").trim();
          const wsCurNid2 = String(currentRootNodeIdRef.current || "").trim();
          if (wsPayloadNid2 && wsCurNid2 && wsPayloadNid2 !== wsCurNid2) { return; }
          if (
            typeof payload?.session_key === "string" &&
            typeof payload?.root_id === "string"
          ) {
            const rootID = payload.root_id;
            const sessionKey = payload.session_key;
            setMultiProjectSessionPending(rootID, sessionKey, true);
            const exchange = payload.exchange;
            const sessionMeta = payload.session;
            const cacheKey = rootSessionKey(rootID, sessionKey);
            console.info("[session/ws] user_message", {
              rootID,
              sessionKey,
              sessionAgent: sessionMeta?.agent || "",
              exchangeAgent: exchange?.agent || "",
              cachedAgent: (sessionCacheRef.current[cacheKey] as any)?.agent || "",
              currentAgent:
                currentSessionRef.current?.key === sessionKey
                  ? ((currentSessionRef.current as any)?.agent || "")
                  : "",
              selectedAgent:
                (selectedSessionRef.current?.key || selectedSessionRef.current?.session_key) === sessionKey
                  ? ((selectedSessionRef.current as any)?.agent || "")
                  : "",
            });
            const cached =
              sessionCacheRef.current[cacheKey] ||
              ({
                key: sessionKey,
                type: sessionMeta?.type || "chat",
                agent: sessionMeta?.agent || exchange?.agent || "",
                model: sessionMeta?.model || exchange?.model || "",
                mode: sessionMeta?.mode || exchange?.mode || "",
                effort: sessionMeta?.effort || exchange?.effort || "",
                fast_service:
                  normalizeFastService(sessionMeta?.fast_service) ||
                  normalizeFastService(exchange?.fast_service),
                  plan_mode:
                    typeof sessionMeta?.plan_mode === "boolean"
                      ? sessionMeta.plan_mode
                      : false,
                name: sessionMeta?.name || t("session.new"),
                created_at:
                  sessionMeta?.created_at ||
                  exchange?.timestamp ||
                  new Date().toISOString(),
                updated_at:
                  sessionMeta?.updated_at ||
                  exchange?.timestamp ||
                  new Date().toISOString(),
                exchanges: [],
              } as any);
            const prevExchanges = Array.isArray((cached as any).exchanges)
              ? ((cached as any).exchanges as Exchange[])
              : [];
            const incomingUserSeq = Number((exchange as any)?.seq || 0);
            // 服务端下发的 seq 是「该用户消息已持久化」的权威标记。跨端时间戳永不相等
            // （客户端 ISO 毫秒 vs 服务端 RFC3339Nano），故不能按时间戳判重：
            //   1) 已有同 seq 条目 → 幂等就地更新（重连重放等）；
            //   2) 已有同 role+content 且尚无 seq 的乐观条目 → 就地转正（写入 seq）；
            //   3) 都没有 → 追加（带 seq 即已持久化条目，无 seq 则仍作瞬时项由 overlay 渲染）。
            const seqIndex =
              incomingUserSeq > 0
                ? prevExchanges.findIndex(
                    (item) => Number((item as any)?.seq || 0) === incomingUserSeq,
                  )
                : -1;
            let mergeIndex = seqIndex;
            if (mergeIndex < 0) {
              for (let i = prevExchanges.length - 1; i >= 0; i -= 1) {
                const item = prevExchanges[i];
                if (
                  item.role === "user" &&
                  item.content === exchange?.content &&
                  !Number((item as any)?.seq || 0)
                ) {
                  mergeIndex = i;
                  break;
                }
              }
            }
            const pushExchange = {
              role: "user",
              agent: exchange?.agent || "",
              model: exchange?.model || "",
              model_display_name: exchange?.model_display_name || "",
              mode: exchange?.mode || "",
              effort: exchange?.effort || "",
              fast_service: exchange?.fast_service || "",
              content: exchange?.content || "",
              timestamp:
                exchange?.timestamp || new Date().toISOString(),
              pending_ack: false,
              ...(incomingUserSeq > 0 ? { seq: incomingUserSeq } : {}),
            } as Exchange;
            const nextExchanges =
              mergeIndex >= 0
                ? [
                    ...prevExchanges.slice(0, mergeIndex),
                    {
                      ...prevExchanges[mergeIndex],
                      ...(incomingUserSeq > 0 ? { seq: incomingUserSeq } : {}),
                      timestamp:
                        exchange?.timestamp || prevExchanges[mergeIndex].timestamp,
                      model: exchange?.model || prevExchanges[mergeIndex].model,
                      model_display_name:
                        exchange?.model_display_name ||
                        prevExchanges[mergeIndex].model_display_name,
                      mode: exchange?.mode || prevExchanges[mergeIndex].mode,
                      effort:
                        exchange?.effort || prevExchanges[mergeIndex].effort,
                      fast_service:
                        exchange?.fast_service ||
                        prevExchanges[mergeIndex].fast_service,
                      pending_ack: false,
                    },
                    ...prevExchanges.slice(mergeIndex + 1),
                  ]
                : [...prevExchanges, pushExchange];
            sessionCacheRef.current[cacheKey] = {
              ...(cached as any),
              ...(sessionMeta || {}),
              key: sessionKey,
              agent:
                sessionMeta?.agent ||
                exchange?.agent ||
                (cached as any).agent ||
                "",
              model:
                sessionMeta?.model ||
                exchange?.model ||
                (cached as any).model ||
                "",
              mode:
                sessionMeta?.mode ||
                exchange?.mode ||
                (cached as any).mode ||
                "",
              effort:
                sessionMeta?.effort ||
                exchange?.effort ||
                (cached as any).effort ||
                "",
              fast_service:
                normalizeFastService(sessionMeta?.fast_service) ||
                normalizeFastService(exchange?.fast_service) ||
                normalizeFastService((cached as any).fast_service),
                plan_mode:
                  typeof sessionMeta?.plan_mode === "boolean"
                    ? sessionMeta.plan_mode
                    : !!(cached as any).plan_mode,
              exchanges: nextExchanges,
              model_display_name:
                sessionMeta?.model_display_name ||
                exchange?.model_display_name ||
                (cached as any).model_display_name ||
                "",
              updated_at:
                sessionMeta?.updated_at ||
                exchange?.timestamp ||
                new Date().toISOString(),
            } as Session;
            const runtimeAgent = sessionMeta?.agent || exchange?.agent || "";
            if (runtimeAgent) {
              updateSessionAgentForKey(
                rootID,
                sessionKey,
                runtimeAgent,
                sessionMeta?.model || exchange?.model || "",
                sessionMeta?.mode || exchange?.mode || "",
                sessionMeta?.effort || exchange?.effort || "",
                normalizeFastService(sessionMeta?.fast_service) ||
                  normalizeFastService(exchange?.fast_service),
                typeof sessionMeta?.plan_mode === "boolean"
                  ? sessionMeta.plan_mode
                  : undefined,
              );
            }
            bumpCacheVersion();
            const newest2 = sessionsRef.current[0]?.updated_at || "";
            void loadSessionsForRoot(
              rootID,
              newest2 ? { afterTime: newest2 } : { replace: true },
            );
            if (multiProjectSessionsEnabled) {
              void loadMultiProjectSessionGroups();
            }
            }
      },
      "task.updated": (event: any, payload: any) => {
          if (
            typeof payload?.root_id === "string" &&
            payload.root_id === currentRootIdRef.current &&
            typeof payload?.task?.id === "string"
          ) {
            // 跨节点隔离：同名项目在另一节点的任务推送不污染当前视图
            const payloadNid = String((payload as any)?.nodeId || (payload as any)?._nodeId || (payload as any)?.task?._nodeId || "").trim();
            const curNid = String(currentRootNodeIdRef.current || "").trim();
            if (payloadNid && curNid && payloadNid !== curNid) { return; }
            const nextTask = payload.task as KanbanTask;
            const detail = payload.detail as TaskDetail | undefined;
            if (detail?.task?.id) {
              applyTaskDetails(payload.root_id, [detail]);
            } else {
              const current = taskDetailsByIdRef.current[nextTask.id];
              applyTaskDetails(payload.root_id, [{
                task: nextTask,
                stage_runs: current?.stage_runs || [],
                events: current?.events || [],
              }]);
            }
            if (nextTask.worktree_path) {
              void refreshTaskWorktree(payload.root_id, nextTask.worktree_path, false);
            }
          }
      },
      "session.meta.updated": (event: any, payload: any) => {
          if (
            typeof payload?.root_id === "string" &&
            typeof payload?.session?.key === "string"
          ) {
            const rootID = payload.root_id;
            const sessionKey = payload.session.key;
            const cacheKey = rootSessionKey(rootID, sessionKey);
            const cached =
              sessionCacheRef.current[cacheKey] ||
              ({
                key: sessionKey,
                root_id: rootID,
                type: normalizeMode(payload.session.type),
                name:
                  typeof payload.session.name === "string"
                    ? payload.session.name
                    : "",
                created_at: payload.session.updated_at || new Date().toISOString(),
                updated_at: payload.session.updated_at || new Date().toISOString(),
                exchanges: [],
              } as Session);
            sessionCacheRef.current[cacheKey] = {
                ...cached,
                name:
                  typeof payload.session.name === "string"
                    ? payload.session.name
                    : cached.name,
                agent:
                  typeof payload.session.agent === "string"
                    ? payload.session.agent
                    : (cached as any).agent,
                model:
                  typeof payload.session.model === "string"
                    ? payload.session.model
                    : (cached as any).model,
                mode:
                  typeof payload.session.mode === "string"
                    ? payload.session.mode
                    : (cached as any).mode,
                effort:
                  typeof payload.session.effort === "string"
                    ? payload.session.effort
                    : (cached as any).effort,
                fast_service:
                  normalizeFastService(payload.session.fast_service) ||
                  normalizeFastService((cached as any).fast_service),
                plan_mode:
                  typeof payload.session.plan_mode === "boolean"
                    ? payload.session.plan_mode
                    : !!(cached as any).plan_mode,
                parent_session_key:
                  typeof payload.session.parent_session_key === "string"
                    ? payload.session.parent_session_key
                    : (cached as any).parent_session_key,
                parent_tool_call_id:
                  typeof payload.session.parent_tool_call_id === "string"
                    ? payload.session.parent_tool_call_id
                    : (cached as any).parent_tool_call_id,
                source:
                  typeof payload.session.source === "string"
                    ? payload.session.source
                    : (cached as any).source,
                task_id:
                  typeof payload.session.task_id === "string"
                    ? payload.session.task_id
                    : (cached as any).task_id,
                related_worktree:
                  payload.session.related_worktree !== undefined
                    ? payload.session.related_worktree
                    : (cached as any).related_worktree,
                pinned_at:
                  payload.session.pinned_at !== undefined
                    ? payload.session.pinned_at || undefined
                    : (cached as any).pinned_at,
                updated_at: payload.session.updated_at || cached.updated_at,
              } as Session;
            bumpCacheVersion();
            if (
              (selectedSessionRef.current?.key ||
                selectedSessionRef.current?.session_key) === sessionKey
            ) {
              setSelectedSession((prev) =>
                prev
                  ? ({
                      ...(prev as any),
                      name:
                        typeof payload.session.name === "string"
                          ? payload.session.name
                          : prev.name,
                      agent:
                        typeof payload.session.agent === "string"
                          ? payload.session.agent
                          : (prev as any).agent,
                      model:
                        typeof payload.session.model === "string"
                          ? payload.session.model
                          : (prev as any).model,
                      mode:
                        typeof payload.session.mode === "string"
                          ? payload.session.mode
                          : (prev as any).mode,
                      effort:
                        typeof payload.session.effort === "string"
                          ? payload.session.effort
                          : (prev as any).effort,
                      fast_service:
                        normalizeFastService(payload.session.fast_service) ||
                        normalizeFastService((prev as any).fast_service),
                      plan_mode:
                        typeof payload.session.plan_mode === "boolean"
                          ? payload.session.plan_mode
                          : !!(prev as any).plan_mode,
                      parent_session_key:
                        typeof payload.session.parent_session_key === "string"
                          ? payload.session.parent_session_key
                          : (prev as any).parent_session_key,
                      parent_tool_call_id:
                        typeof payload.session.parent_tool_call_id === "string"
                          ? payload.session.parent_tool_call_id
                          : (prev as any).parent_tool_call_id,
                      source:
                        typeof payload.session.source === "string"
                          ? payload.session.source
                          : (prev as any).source,
                      task_id:
                        typeof payload.session.task_id === "string"
                          ? payload.session.task_id
                          : (prev as any).task_id,
                      related_worktree:
                        payload.session.related_worktree !== undefined
                          ? payload.session.related_worktree
                          : (prev as any).related_worktree,
                      pinned_at:
                        payload.session.pinned_at !== undefined
                          ? payload.session.pinned_at || undefined
                          : (prev as any).pinned_at,
                      updated_at: payload.session.updated_at || prev.updated_at,
                    } as SessionItem)
                  : prev,
              );
            }
            if (boundSessionByRootRef.current[scopedRootKey(rootID)] === sessionKey) {
              const latest = sessionCacheRef.current[cacheKey];
              if (latest) {
                setDrawerSessionForRoot(rootID, latest);
              }
            }
            const relatedWorktreePath =
              typeof payload.session.related_worktree?.path === "string"
                ? payload.session.related_worktree.path.trim()
                : "";
            if (relatedWorktreePath) {
              void refreshTaskWorktree(rootID, relatedWorktreePath, false);
            }
            // afterTime 增量按 updated_at 严格大于排除刚创建/刚更新的会话（乐观项 timestamp 与
            // 服务端 updated_at 的竞态，session.done 处有同款注释）：新 key 首次经 WS 露面时
            // 必须 replace 全量重拉，否则要等手动同步才出现在列表里。
            const listHasKey = sessionsRef.current.some(
              (item) =>
                String(item.key || item.session_key || "") === sessionKey,
            );
            if (listHasKey) {
              const newest = sessionsRef.current[0]?.updated_at || "";
              void loadSessionsForRoot(
                rootID,
                newest ? { afterTime: newest } : { replace: true },
              );
            } else {
              void loadSessionsForRoot(rootID, { replace: true });
            }
            if (multiProjectSessionsEnabled) {
              void loadMultiProjectSessionGroups();
            }
          }
      },
      "session.related_files.updated": (event: any, payload: any) => {
          const rootID =
            typeof payload?.root_id === "string" ? payload.root_id : "";
          const sessionKey =
            typeof payload?.session_key === "string" ? payload.session_key : "";
          if (rootID && sessionKey) {
            void refreshSessionRelatedFiles(rootID, sessionKey);
            refreshTasksForRelatedSession(rootID, sessionKey);
            const cachedSession =
              sessionCacheRef.current[rootSessionKey(rootID, sessionKey)];
            const parentSessionKey = String(
              cachedSession?.parent_session_key || "",
            ).trim();
            if (parentSessionKey) {
              void refreshSessionRelatedFiles(rootID, parentSessionKey);
              refreshTasksForRelatedSession(rootID, parentSessionKey);
            }
            if (payload?.related_worktree && typeof payload.related_worktree === "object") {
              updateSessionRelatedWorktreeForKey(
                rootID,
                sessionKey,
                payload.related_worktree as RelatedWorktree,
              );
            }
          }
          return;
      },
      "file.changed.batch": (event: any, payload: any) => {
          handleFileChangedBatch(payload);
      },
      "file.changed": (event: any, payload: any) => {
          handleFileChanged(payload);
      },
      "agent.status.changed": (event: any, payload: any) => {
          setAgentsVersion((v) => v + 1);
      },
      "app.update": (event: any, payload: any) => {
          setUpdateState(normalizeUpdateState(payload?.state as UpdateState));
      },
      "github.import": (event: any, payload: any) => {
          const status = (payload?.status || {}) as any;
          const taskID = typeof status?.task_id === "string" ? status.task_id : "";
          if (!taskID) {
            return;
          }
          setGitHubImportState((prev) => {
            if (prev.taskId && prev.taskId !== taskID) {
              return prev;
            }
            const nextStatus = String(status?.status || "");
            const done = nextStatus === "done";
            const failed = nextStatus === "failed";
            return {
              ...prev,
              taskId: taskID,
              status: nextStatus,
              message: String(status?.message || ""),
              running: !done && !failed,
              submitting: false,
              done,
              error: failed ? String(status?.message || t("root.githubImportFailed")) : "",
            };
          });
          if (String(status?.status || "") === "done") {
            setProjectAddMode(null);
            void refreshManagedRoots();
            const rootID = typeof status?.root_id === "string" ? status.root_id : "";
            if (rootID) {
              void actionHandlersRef.current.open_dir({
                path: rootID,
                root: rootID,
                isRoot: true,
              });
            }
          }
          return;
      },
    } as Record<string, (event: any, payload: any) => void>;
  }, [
    loadMultiProjectSessionGroups,
    loadSessionsForRoot,
    scheduleSessionListReload,
    multiProjectSessionsEnabled,
    refreshMultiProjectReplyingSessions,
    rootSessionKey,
    resolveRootForSessionKey,
    promotePendingSessionForRoot,
    appendAgentChunkForSession,
    appendThoughtChunkForSession,
    appendToolCallForSession,
    appendTodoUpdateForSession,
    appendPlanUpdateForSession,
    appendCompactNoticeForSession,
    clearSessionStale,
    markSessionPending,
    markSessionStale,
    setSelectedPendingByKey,
    setBoundSessionForRoot,
    setDrawerSessionForRoot,
    setMultiProjectSessionPending,
    refreshManagedRoots,
    invalidatePluginsForRoot,
    refreshTreeDir,
    refreshCurrentFileContent,
    refreshGitStatus,
    updateSessionRelatedWorktreeForKey,
    updateSessionRelatedFilesForKey,
    refreshTasksForRelatedSession,
    updateSessionAgentForKey,
    treeCacheKey,
    t,
  ]);
  const wsHandlersRef = useRef(wsHandlers);
  useEffect(() => {
    wsHandlersRef.current = wsHandlers;
  }, [wsHandlers]);
  useEffect(() => {
    if (!currentRootId) return;
    cancelledRef.current = false;
    const unsubscribeEvents = sessionService.subscribeEvents((event) => {
      wsHandlersRef.current[event.type]?.(event, (event.payload || {}) as any);
    });
    void loadSessionsForRoot(currentRootId, { replace: true });
    return () => {
      cancelledRef.current = true;
      if (sessionListReloadTimerRef.current) {
        window.clearTimeout(sessionListReloadTimerRef.current);
        sessionListReloadTimerRef.current = null;
      }
      unsubscribeEvents();
    };
  }, [currentRootId, loadSessionsForRoot]);
}
