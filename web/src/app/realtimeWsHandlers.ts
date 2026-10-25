import type { Dispatch, RefObject, SetStateAction } from "react";
import type { I18nContextValue } from "../i18n";
import type { GitHubImportState, ProjectAddMode } from "../shared/projectAddTypes";
import type { FileEntry } from "../services/prefs/directorySort";
import type { FilePayload } from "../services/file";
import type { GitDiffPayload, GitStatusPayload } from "../services/git";
import type { QueuedUserMessage, RelatedFile, RelatedWorktree, Session, TokenUsage } from "../services/session";
import type { UpdateState } from "../services/update";
import { unreadCompletedAdd, unreadKey } from "../shared/unreadCompleted";
import type { ManagedRootPayload } from "./appMisc";
import type { Exchange, MultiProjectSessionGroup, PendingSend, SessionItem, SessionQueueItem, SlashCommandResult, WSStatus } from "./appSession";
import { dropTransientExchanges, isSamePendingEcho } from "../services/session";
import { setRootNodeMap } from "../services/net/rootNode";
import { applyNodesFromServer, syncNodesFromServer } from "../services/net/nodeRegistry";
import { mapManagedRootsToEntries, normalizeUpdateState } from "./appMisc";
import { rootNodeKey } from "./appPath";
import { normalizeMode } from "./appSession";
import { normalizeFastService } from "./appTask";
import type { RealtimeEventsContext } from "./useRealtimeEventsContext";
import { createSessionMetaHandlers } from "./realtimeWsSessionMeta";
import { handleSessionUserMessage } from "./realtimeWsUserMessage";
import { handleSessionAccepted } from "./realtimeWsSessionAccepted";
import { handleRootChanged } from "./realtimeWsRootChanged";
import { createRealtimeWsTaskHandlers } from "./realtimeWsTaskHandlers";

/**
 * `useRealtimeEvents` 内部、wsHandlers 闭包要用的 9 个局部函数。
 *
 * 它们都关闭在 hook 的 state / ref / ctx 上，没法独立成模块；但 wsHandlers 那张
 * 957 行的表本身可以整体搬走 —— 只要把这 9 个名字当参数传进来。类型全部由 tsc
 * 从 hook 里的声明反推（见 app-tsx-split-lessons-and-next-plan.md 的工具链）。
 */
export type RealtimeWsHandlersLocal = {
  reloadSessionForReplay: (rootID: string, sessionKey: string) => Promise<void>;
  getReplayTargetsForRoot: (rootID: string) => string[];
  replayTargetsForAllRoots: () => void;
  refreshSessionRelatedFiles: (rootID: string, sessionKey: string) => void;
  handleSessionStreamDone: (rootID: string, sessionKey: string) => boolean;
  handleSessionStream: (payload: any) => void;
  handleSlashCommandStream: (payload: any) => void;
  handleSlashCommandDone: (payload: any) => void;
  handleFileChanged: (payload: any) => void;
  handleFileChangedBatch: (payload: any) => void;
};

export function createRealtimeWsHandlers(
  ctx: RealtimeEventsContext,
  local: RealtimeWsHandlersLocal,
): Record<string, (event: any, payload: any) => void> {
  const {
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
  } = local;
  const {
    refs: {
      actionHandlersRef,
      boundSessionByRootRef,
      currentRootIdRef,
      currentRootNodeIdRef,
      currentSessionRef,
      drawerSessionByRootRef,
      managedRootByIdRef,
      managedRootByKeyRef,
      managedRootIdsRef,
      optimisticDequeuedIdsRef,
      pendingBySessionRef,
      pendingDraftRef,
      pendingRequestRef,
      queueFrozenBySessionRef,
      queuedMessagesBySessionRef,
      selectedSessionRef,
      sessionCacheRef,
      sessionsRef,
    },
    setters: {
      setDrawerSessionForRoot,
      setGitHubImportState,
      setMultiProjectSessionGroups,
      setMultiProjectSessionPending,
      setProjectAddMode,
      setQueueVersion,
      setRootEntries,
      setAgentsVersion,
      setSelectedSession,
      setStatus,
      setUpdateState,
    },
    actions: {
      applyManagedRootRename,
      bumpCacheVersion,
      getNodeIdForRoot,
      loadManagedRootPayloads,
      loadSessionsForRoot,
      markSessionStale,
      playNotificationSound,
      promotePendingSessionForRoot,
      refreshManagedRoots,
      refreshMultiProjectReplyingSessions,
      refreshTasksForRelatedSession,
      resolveRootForSessionKey,
      scheduleMultiProjectSessionReload,
      scheduleSessionListReload,
      updateSessionAgentForKey,
      updateSessionRelatedWorktreeForKey,
    },
    values: {
      handleImportedSessionConfirmed,
      multiProjectSessionsEnabled,
      rootSessionKey,
      scopedRootKey,
      t,
    },
  } = ctx;

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
            scheduleMultiProjectSessionReload();
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
            scheduleMultiProjectSessionReload();
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
              scheduleMultiProjectSessionReload();
            }
          }).catch(() => {});
      },
      "root.changed": (event: any, payload: any) => handleRootChanged(ctx, event, payload),
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
            scheduleMultiProjectSessionReload();
          }
          return;
      },
      "session.created": (event: any, payload: any) => {
          const rootID =
            typeof payload?.root_id === "string" ? payload.root_id : "";
          if (rootID && rootID === currentRootIdRef.current) {
            void scheduleSessionListReload(rootID, { replace: true });
          }
          // 走合并窗口而不是直接重拉：created 是所有建会话路径的统一信号
          // （看板任务 / 定时任务 / 子会话 / fork），一次动作可能连发好几条，
          // 每次都裸调会逐节点扇出一次 /api/sessions?multi_root=1。
          if (rootID && multiProjectSessionsEnabled) {
            scheduleMultiProjectSessionReload();
          }
          return;
      },
      // 服务端搬完转录（worktree → 主 checkout，见 AGENTS.md 事实 14）后广播这一条：
      // 前端手里还是**旧视图**（worktree 标签、`agent_session_id`），必须失效并重拉。
      // 这条帧此前**没有任何消费者** —— 服务端注释写的意图「广播一次强制刷新」因此从未生效
      // （`tests/message-mechanism-merges.test.mjs` 的【红·⑫】就是钉这个：发出但没人接的帧）。
      "session.repointed": (event: any, payload: any) => {
          const rootID = typeof payload?.root_id === "string" ? payload.root_id : "";
          const sessionKey =
            typeof payload?.session_key === "string" ? payload.session_key : "";
          if (!rootID || !sessionKey) return;
          markSessionStale(rootID, sessionKey);
          if (getReplayTargetsForRoot(rootID).includes(sessionKey)) {
            void reloadSessionForReplay(rootID, sessionKey);
          }
          return;
      },
      // 服务端在 `session.ready` 之后回的整批快照前，先发这一帧（见 session.ts 的
      // emitDecrypted：批处理帧在那里被拆成「一帧 reset + N 帧单事件」，两条分发路径
      // 从此只有一种形状）。
      //
      // **reset 的全部语义就是「清空瞬时尾巴」。** 清完之后紧接着应用的快照事件，
      // 作用对象必然是一个已知为空的尾巴，所以结果 = 该轮事件列表的纯函数，
      // 与客户端此前见过什么、有没有漏收无关。重复渲染那一整类 bug 由此在结构上消失。
      "session.stream.reset": (event: any, payload: any) => {
          const wsPayloadNid = String((payload as any)?._nodeId || (payload as any)?.nodeId || "").trim();
          const wsCurNid = String(currentRootNodeIdRef.current || "").trim();
          if (wsPayloadNid && wsCurNid && wsPayloadNid !== wsCurNid) { return; }
          const rootID = typeof payload?.root_id === "string" ? payload.root_id : "";
          const sessionKey =
            typeof payload?.session_key === "string" ? payload.session_key : "";
          if (!rootID || !sessionKey) return;
          const cacheKey = rootSessionKey(rootID, sessionKey);
          const cached = sessionCacheRef.current[cacheKey];
          if (!cached || cached.key !== sessionKey) return;
          const next = dropTransientExchanges(cached as Session) as Session;
          if (next === cached) return;
          sessionCacheRef.current[cacheKey] = next;
          bumpCacheVersion();
          return;
      },
      "session.stream": (event: any, payload: any) => {
          // 单事件帧（批处理帧已在 session.ts 的 emitDecrypted 里展开，这里只见单条）。
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
      "session.accepted": (event: any, payload: any) => handleSessionAccepted(ctx, event, payload),
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
            playNotificationSound("error");
          }
          const latestDrawer = drawerSessionByRootRef.current[scopedRootKey(rootID)];
          if (targetKey && latestDrawer?.key === targetKey) {
            const exchanges = Array.isArray((latestDrawer as any).exchanges)
              ? ((latestDrawer as any).exchanges as Exchange[]).map(
                  (exchange) =>
                    isSamePendingEcho(exchange, pending)
                      ? { ...exchange, pending_ack: false }
                      : exchange,
                )
              : [];
            setDrawerSessionForRoot(rootID, {
              ...(latestDrawer as any),
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
            playNotificationSound("complete");
            // 未读只标「没在看的那条」：正开着的会话用户已经看到了，标了还得再点一次才灭。
            const nodeId = wsPayloadNid || getNodeIdForRoot(rootID) || "";
            const viewing =
              document.visibilityState === "visible" &&
              String(selectedSessionRef.current?.key || selectedSessionRef.current?.session_key || "") === sessionKey;
            if (!viewing) unreadCompletedAdd(unreadKey(nodeId, rootID, sessionKey));
            setMultiProjectSessionPending(rootID, sessionKey, false);
            // 重锚定已并入这次收尾（handleSessionStreamDone 内部按 replay 目标判断），
            // 不再在这里单独猜一遍「此刻还在不在流」—— 那个时序代理在监听者里恒为
            // true，于是正常回合的重锚定恒被跳过，而尾巴又已被清 ⇒ 正文当场消失。
            // 见 docs/session-streaming-rework.md §6.2。
            handleSessionStreamDone(rootID, sessionKey);
            // 会话刚结束，服务端 updated_at/context_window 已持久化。
            // afterTime 增量会被"updated_at 严格大于旧 newest"排除刚结束的会话（竞态），
            // 必须 replace 全量重拉才能带上最新 meta。频率低 + 300ms debounce，成本可接受。
            void scheduleSessionListReload(rootID, { replace: true });
            if (multiProjectSessionsEnabled) {
              scheduleMultiProjectSessionReload();
            }
          } else if (currentRootIdRef.current) {
            void scheduleSessionListReload(currentRootIdRef.current, {
              replace: true,
            });
            if (multiProjectSessionsEnabled) {
              void refreshMultiProjectReplyingSessions();
              scheduleMultiProjectSessionReload();
            }
          }
          return;
      },
      "session.user_message": (event: any, payload: any) => handleSessionUserMessage(ctx, event, payload),
      ...createRealtimeWsTaskHandlers(ctx),
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
    ...createSessionMetaHandlers(ctx, local),
  };
}
