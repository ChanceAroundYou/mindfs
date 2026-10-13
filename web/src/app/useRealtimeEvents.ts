import { useEffect, useMemo, useRef } from "react";
import type { I18nContextValue } from "../i18n";
import type { GitHubImportState, ProjectAddMode } from "../components/file/ProjectAddPopover";
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
import { reportError } from "../services/net/error";
import { mergeSessionItems } from "../services/session/sessionListMerge";
import { streamEventTimestamp } from "../services/session/sessionDuration";
import { mapManagedRootsToEntries, normalizeUpdateState } from "./appMisc";
import { rootNodeKey } from "./appPath";
import { hasSessionExchanges, normalizeMode, toSessionItem } from "./appSession";
import { normalizeFastService } from "./appTask";
import type { Exchange, SessionQueueItem } from "./appSession";
import type { KanbanTask } from "../services/tasks";

import { createRealtimeWsHandlers } from "./realtimeWsHandlers";
import { createRealtimeWsFileChangedHandlers } from "./realtimeWsFileChangedHandlers";
import { createRealtimeWsSlashCommandHandlers } from "./realtimeWsSlashCommandHandlers";

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

    const handleSessionStream = (payload: any) => {
      const wsNid = String((payload as any)?._nodeId || (payload as any)?.nodeId || "").trim();
      const curNid = String(currentRootNodeIdRef.current || "").trim();
      const streamKey =
        typeof payload?.session_key === "string" ? payload.session_key : "";
      const activeRoot =
        typeof payload?.root_id === "string" && payload.root_id
          ? payload.root_id
          : resolveRootForSessionKey(streamKey) || currentRootIdRef.current;
      if (!streamKey || !activeRoot) return;
      const event = payload.event;
      // Apply client-only activity before filtering stream content to the active node.
      if (event?.type && event.type !== "message_done") {
        const eventTime = Date.parse(streamEventTimestamp(event, new Date().toISOString())) || Date.now();
        const activityAt = new Date(Math.floor(eventTime / 100) * 100).toISOString();
        const activityNode = wsNid || getNodeIdForRoot(activeRoot) || "";
        const advanceItem = (item: SessionItem): SessionItem => {
          const currentTime = Math.max(
            Date.parse(String(item.updated_at || "")) || 0,
            Date.parse(String(item.activity_at || "")) || 0,
          );
          return Date.parse(activityAt) > currentTime ? { ...item, activity_at: activityAt } : item;
        };
        setMultiProjectSessionGroups((prev) => {
          let changed = false;
          const next = prev.map((group) => {
            const groupNode = String(group._nodeId || "").trim();
            if (
              group.rootId !== activeRoot ||
              (activityNode && groupNode && activityNode !== groupNode)
            ) return group;
            const groupTime = Math.max(
              Date.parse(String(group.latestSessionTime || "")) || 0,
              Date.parse(String(group.activity_at || "")) || 0,
            );
            const sessions = group.sessions.map((item) => {
              if ((item.key || item.session_key) !== streamKey) return item;
              const itemNode = String(item._nodeId || groupNode).trim();
              if (activityNode && itemNode && activityNode !== itemNode) return item;
              const advanced = advanceItem(item);
              if (advanced !== item) changed = true;
              return advanced;
            });
            const activity_at = Date.parse(activityAt) > groupTime ? activityAt : group.activity_at;
            if (activity_at !== group.activity_at || sessions.some((item, i) => item !== group.sessions[i])) {
              changed = true;
              return { ...group, activity_at, sessions: mergeSessionItems(sessions, []) };
            }
            return group;
          });
          return changed ? next : prev;
        });
        if (activeRoot === currentRootIdRef.current && (!wsNid || !curNid || wsNid === curNid)) {
          setSessions((prev) => mergeSessionItems(prev.map((item) => {
            if ((item.key || item.session_key) !== streamKey || (item.root_id && item.root_id !== activeRoot)) return item;
            const itemNode = String(item._nodeId || "").trim();
            if (wsNid && itemNode && itemNode !== wsNid) return item;
            return advanceItem(item);
          }), []));
        }
      }
      if (wsNid && curNid && wsNid !== curNid) return;
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
          // pending 不写在会话对象上（唯一真相是 multiProjectPendingByKey，由上面的
          // markSessionPending 负责写入）。
          setDrawerSessionForRoot(activeRoot, seeded);
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
            event.timestamp,
            event.mindfs_id,
          );
          break;
        case "thought_chunk":
          appendThoughtChunkForSession(
            activeRoot,
            streamKey,
            event.data?.content || "",
            event.data?.id || "",
            event.mindfs_id,
          );
          break;
        case "tool_call":
          appendToolCallForSession(
            activeRoot,
            streamKey,
            event.data || {},
            false,
            event.mindfs_id,
          );
          break;
        case "tool_call_update":
          appendToolCallForSession(
            activeRoot,
            streamKey,
            event.data || {},
            true,
            event.mindfs_id,
          );
          break;
        case "todo_update":
          appendTodoUpdateForSession(
            activeRoot,
            streamKey,
            event.data || {},
            event.mindfs_id,
          );
          break;
        case "plan_update":
          appendPlanUpdateForSession(
            activeRoot,
            streamKey,
            event.data || {},
            event.mindfs_id,
          );
          break;
        case "compact_notice": {
          appendCompactNoticeForSession(
            activeRoot,
            streamKey,
            event.data || {},
            event.mindfs_id,
          );
          // **这里曾经清掉该会话所有瞬时行，已删除（2026-10-06）。** 原注释给的两条依据
          // 都不成立：
          //   · 「服务端历史整个重置了」—— 交换 JSONL 是 **append-only**（唯一 `os.Remove`
          //     在两处 `DeleteSession`），compact 只写一条 `CompactNotice` aux；
          //   · 「此刻没有在途回合，不存在误伤」—— `compact_notice` 是**轮内**流事件
          //     （本 `handleSessionStream` 的一个 case），当时正有在途回合。
          // 于是 compact 一到就把**本轮 compact 之前**还没落盘的内容一起丢掉（连刚追加的
          // 这条通知自己也在 seq=0 里）。陈旧瞬时行由另外两处负责：一轮结束 / reset 的
          // `dropTransientExchanges`，以及加载时 `composeLoadedExchanges(inFlight=false)`。
          // 见 docs/message-mechanisms.md 冲突⑨ 与 tests 里的【红·⑨c】。
          break;
        }
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
