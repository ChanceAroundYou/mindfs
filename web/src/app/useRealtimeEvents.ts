import { useEffect, useMemo, useRef } from "react";
import type { Dispatch, RefObject, SetStateAction } from "react";
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
import { buildGitDiffCacheSignature, fetchGitDiff } from "../services/git";
import { invalidateFileCache } from "../services/file";
import { dropTransientExchanges, isSamePendingEcho, settlePendingAcks } from "../services/session";
import { reportError } from "../services/net/error";
import { setRootNodeMap } from "../services/net/rootNode";
import { applyNodesFromServer, syncNodesFromServer } from "../services/net/nodeRegistry";
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
    /** 主区是否停在跨项目工作台。工作台要收全部项目的 task.updated，不能只听当前项目。 */
    workspaceOpenRef: RefObject<boolean>;
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
    // 「标题 + 正文 + 详情清单」那个弹窗。收尾撞上冲突要走它：仓库停在 MERGE_HEAD，
    // 得把冲突文件逐条列出来，toast 里塞不下、也不会自己消失。
    setTaskSessionErrorDialog: Dispatch<SetStateAction<{ title: string; message: string; details: string[] } | null>>;
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
    /** 同上，但带 300ms 合并窗口——「新会话出现」这类会连发的事件用它 */
    scheduleMultiProjectSessionReload: () => void;
    loadSessionsForRoot: (rootID: string, options?: { beforeTime?: string; afterTime?: string; replace?: boolean; force?: boolean; }) => Promise<void>;
    markSessionPending: (rootID: string, sessionKey: string) => void;
    markSessionStale: (rootID: string | null | undefined, sessionKey: string | null | undefined) => void;
    playCompletionSound: () => void;
    /** 摘掉一个任务在本机的全部痕迹（task.deleted）。作用域由 rootId 划定。 */
    pruneTaskDetails: (rootId: string, keepTaskIds: Iterable<string>) => void;
    /** 重拉工作台结构。转发 ref，为 null 表示工作台没打开，调用方无需自己判。 */
    refreshWorkspaceBoard: () => void;
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

import { createRealtimeWsHandlers } from "./realtimeWsHandlers";

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
      setSelectedPendingByKey,
      setSelectedSession,
      setSessions,
      setSlashCommandResults,
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
      invalidatePluginsForRoot,
      loadManagedRootPayloads,
      loadMultiProjectSessionGroups,
      scheduleMultiProjectSessionReload,
      loadSessionsForRoot,
      markSessionPending,
      markSessionStale,
      playCompletionSound,
      pruneTaskDetails,
      promotePendingSessionForRoot,
      refreshCurrentFileContent,
      refreshGitStatus,
      refreshManagedRoots,
      refreshMultiProjectReplyingSessions,
      refreshTaskWorktree,
      refreshTasksForRelatedSession,
      refreshTreeDir,
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
        const base = clearPendingAck({
          ...(cached as any),
          pending: false,
        } as Session);
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
      if (willReanchor) {
        void reloadSessionForReplay(rootID, sessionKey);
      }
      return true;
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
        case "compact_notice": {
          appendCompactNoticeForSession(
            activeRoot,
            streamKey,
            event.data || {},
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
    return createRealtimeWsHandlers(ctx, {
      reloadSessionForReplay,
      getReplayTargetsForRoot,
      replayTargetsForAllRoots,
      refreshSessionRelatedFiles,
      handleSessionStreamDone,
      handleSessionStream,
      handleSlashCommandStream,
      handleSlashCommandDone,
      handleFileChanged,
      handleFileChangedBatch,
    });
  }, [
    scheduleMultiProjectSessionReload,
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
    pruneTaskDetails,
    refreshWorkspaceBoard,
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
