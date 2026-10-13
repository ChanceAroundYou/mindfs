import { useEffect, useMemo, useRef } from "react";
import type { I18nContextValue } from "../i18n";
import type { GitHubImportState, ProjectAddMode } from "../shared/projectAddTypes";
import type { FileEntry } from "../services/prefs/directorySort";
import type { FilePayload } from "../services/file";
import type { GitDiffPayload, GitStatusPayload } from "../services/git";
import type { QueuedUserMessage, RelatedFile, RelatedWorktree, Session, TokenUsage } from "../services/session";
import type { TaskDetail } from "../services/tasks";
import type { UpdateState } from "../services/update";
import type { ManagedRootPayload } from "./appMisc";
import type { MultiProjectSessionGroup, PendingSend, SessionItem, SlashCommandResult, WSStatus } from "./appSession";
import type { SessionRuntimeMeta } from "./useSessionStreamCache";
import { dropTransientExchanges, isSamePendingEcho, settlePendingAcks } from "../services/session";
import type { RealtimeEventsContext } from "./useRealtimeEventsContext";
import { setRootNodeMap } from "../services/net/rootNode";
import { applyNodesFromServer, syncNodesFromServer } from "../services/net/nodeRegistry";
import { sessionService, setCachedSessionRelatedFiles } from "../services/session";
import { mapManagedRootsToEntries, normalizeUpdateState } from "./appMisc";
import { rootNodeKey } from "./appPath";
import { hasSessionExchanges, normalizeMode, toSessionItem } from "./appSession";
import { normalizeFastService } from "./appTask";
import type { KanbanTask } from "../services/tasks";

import { createRealtimeWsHandlers } from "./realtimeWsHandlers";
import { createRealtimeWsFileChangedHandlers } from "./realtimeWsFileChangedHandlers";
import { createRealtimeWsSlashCommandHandlers } from "./realtimeWsSlashCommandHandlers";
import { createRealtimeWsSessionStreamHandler } from "./realtimeWsSessionStream";

export function useRealtimeEvents(ctx: RealtimeEventsContext) {
  const {
    refs: {
      actionHandlersRef,
      boundSessionByRootRef,
      cancelRequestedBySessionRef,
      currentRootIdRef,
      currentSessionRef,
      drawerSessionByRootRef,
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
      selectedSessionByRootRef,
      selectedSessionRef,
      sessionCacheRef,
      sessionListReloadTimerRef,
      sessionsRef,
      suppressedAutoBindSessionByRootRef,
      taskDetailsByIdRef,
      workspaceOpenRef,
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
      setSelectedSession,
      setSessions,
      setStatus,
      setTaskSessionErrorDialog,
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
      loadManagedRootPayloads,
      loadMultiProjectSessionGroups,
      scheduleMultiProjectSessionReload,
      loadSessionsForRoot,
      markSessionPending,
      markSessionStale,
      playCompletionSound,
      pruneTaskDetails,
      promotePendingSessionForRoot,
      refreshManagedRoots,
      refreshMultiProjectReplyingSessions,
      refreshTaskWorktree,
      refreshTasksForRelatedSession,
      refreshWorkspaceBoard,
      resolveRootForSessionKey,
      restoreActiveSession,
      scheduleSessionListReload,
      updateSessionAgentForKey,
      updateSessionRelatedFilesForKey,
      updateSessionRelatedWorktreeForKey,
    },
    values: {
      currentRootId,
      handleImportedSessionConfirmed,
      multiProjectSessionsEnabled,
      rootSessionKey,
      scopedRootKey,
      t,
    },
  } = ctx;

  // 2026-09 App.tsx 拆分：34 个 WS 事件处理器整体搬进 useMemo，订阅器只剩「派发」这一件事。
  // 事件回调全同步、无 await，且没有「值变了才动作」的守卫比较，故经 ref 读最新闭包不改变行为。
  const cancelledRef = useRef(false);
  // related-files 刷新去抖：agent 每写一个文件就发一条 session.related_files.updated，
  // 实测 20 分钟内同一会话被拉了 74 次（每秒 4–6 个突发），而回包内容高度重复。
  // 按 rootID::sessionKey 合并 500ms 窗口内的多次触发，只拉一次。
  const relatedFilesRefreshTimers = useRef(new Map<string, number>());
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
    const refreshSessionRelatedFiles = (
      rootID: string,
      sessionKey: string,
    ) => {
      if (!rootID || !sessionKey) return;
      const debounceKey = `${rootID}::${sessionKey}`;
      const existing = relatedFilesRefreshTimers.current.get(debounceKey);
      if (existing) {
        window.clearTimeout(existing);
      }
      const timer = window.setTimeout(() => {
        relatedFilesRefreshTimers.current.delete(debounceKey);
        void (async () => {
          const relatedFiles = await sessionService.getSessionRelatedFiles(
            rootID,
            sessionKey,
            getNodeIdForRoot(rootID),
          );
          if (cancelledRef.current) return;
          await setCachedSessionRelatedFiles(rootID, sessionKey, relatedFiles, getNodeIdForRoot(rootID));
          updateSessionRelatedFilesForKey(rootID, sessionKey, relatedFiles);
        })();
      }, 500);
      relatedFilesRefreshTimers.current.set(debounceKey, timer);
    };
    // 回合收尾的**唯一出口**。返回值 = 这一轮是否真的结束了（false = 队列续跑，仍在流式）。
    //
    // 「瞬时尾巴怎么退役」在这里定，调用方不再各自判断一遍 —— 曾经这个判断有两个来源、
    // 而且顺序相反：调用方先无条件清尾巴，再用 `isSessionStreaming` 猜「要不要重锚定」，
    // 猜错的后果是居中的正文永远不见（见 docs/session-streaming-rework.md §6）。
    const handleSessionStreamDone = (rootID: string, sessionKey: string): boolean => {
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
        return settlePendingAcks(session);
      };
      const queued = queuedMessagesBySessionRef.current[cacheKey] || [];
      const queueFrozen = !!queueFrozenBySessionRef.current[cacheKey];
      const hiddenQueued = optimisticDequeuedIdsRef.current[cacheKey];
      const hasQueuedContinuation =
        (queued.length > 0 && !queueFrozen) ||
        !!(hiddenQueued && hiddenQueued.size > 0);
      if (hasQueuedContinuation && !wasCanceled) {
        markSessionPending(rootID, sessionKey);
        return false;
      }
      // 这个会话接下来会被重锚定（restoreActiveSession 用服务端窗口替换缓存）。
      const willReanchor = getReplayTargetsForRoot(rootID).includes(sessionKey);
      const cached = sessionCacheRef.current[cacheKey];
      if (cached && cached.key === sessionKey) {
        // pending 不在会话对象上（纯派生自 multiProjectPendingByKey，见下方
        // setMultiProjectSessionPending），这里只剩「取消后把 pending_ack 收尾」。
        const base = clearPendingAck(cached as Session);
        // 轮次结束 = 不再在途：服务端已经把这一轮落盘了，缓存里的 seq=0 从此都是残留
        // （见 session.ts 的 dropTransientExchanges）。
        //
        // **但会重锚定的会话不在这里清**：窗口快照一到，载入时的唯一组装规则
        // （composeLoadedExchanges，`pending=false` ⇒ 不接回 seq=0）就把尾巴退役了 ——
        // 那才是唯一的退役时机。先清、再由异步重锚定装持久行，是一个非原子的交接：
        // 拉取失败或这一帧被别的判断拦下，这一轮就**永远不见**（用户报的正是这个形态）。
        sessionCacheRef.current[cacheKey] = (
          willReanchor ? base : dropTransientExchanges(base)
        ) as Session;
        bumpCacheVersion();
      }
      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        const prevRoot =
          (prev?.root_id as string | undefined) || currentRootIdRef.current;
        if (!prev || prevKey !== sessionKey || prevRoot !== rootID) {
          return prev;
        }
        // 同上：没有 pending 可清，只有取消后的 pending_ack 收尾（未取消时原样返回）。
        return clearPendingAck(prev as SessionItem);
      });
      const drawer = drawerSessionByRootRef.current[scopedRootKey(rootID)];
      if (drawer && drawer.key === sessionKey) {
        const latest = wasCanceled
          ? sessionCacheRef.current[cacheKey] || drawer
          : drawer;
        const next = clearPendingAck(latest as Session);
        if (next !== latest) {
          setDrawerSessionForRoot(rootID, next);
        }
      }
      if (currentRootIdRef.current === rootID) {
        setSessions((prev) =>
          prev.map((item) => {
            const itemKey = item.key || item.session_key;
            if (itemKey !== sessionKey) {
              return item;
            }
            return clearPendingAck(item as SessionItem);
          }),
        );
      }
      setMultiProjectSessionPending(rootID, sessionKey, false);
      bumpCacheVersion();
      if (willReanchor) {
        void reloadSessionForReplay(rootID, sessionKey);
      }
      return true;
    };

    const handleSessionStream = createRealtimeWsSessionStreamHandler(ctx, {
      handleSessionStreamDone,
    });
    const slashCommandHandlers = createRealtimeWsSlashCommandHandlers(ctx);
    const fileChangedHandlers = createRealtimeWsFileChangedHandlers(ctx);
    return createRealtimeWsHandlers(ctx, {
      reloadSessionForReplay,
      getReplayTargetsForRoot,
      replayTargetsForAllRoots,
      refreshSessionRelatedFiles,
      handleSessionStreamDone,
      handleSessionStream,
      ...slashCommandHandlers,
      ...fileChangedHandlers,
    });
  }, [
    scheduleMultiProjectSessionReload,
    loadSessionsForRoot,
    scheduleSessionListReload,
    multiProjectSessionsEnabled,
    refreshMultiProjectReplyingSessions,
    getNodeIdForRoot,
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
    setBoundSessionForRoot,
    setDrawerSessionForRoot,
    setMultiProjectSessionPending,
    refreshManagedRoots,
    updateSessionRelatedWorktreeForKey,
    updateSessionRelatedFilesForKey,
    refreshTasksForRelatedSession,
    updateSessionAgentForKey,
    pruneTaskDetails,
    refreshWorkspaceBoard,
    t,
  ]);
  const wsHandlersRef = useRef(wsHandlers);
  useEffect(() => {
    wsHandlersRef.current = wsHandlers;
  }, [wsHandlers]);
  useEffect(() => {
    if (!currentRootId) return;
    cancelledRef.current = false;
    const unsubscribeEvents = sessionService.dispatcher.subscribeEvents((event) => {
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
