import type { Dispatch, RefObject, SetStateAction } from "react";
import type { I18nContextValue } from "../i18n";
import type { GitHubImportState, ProjectAddMode } from "../shared/projectAddTypes";
import type { FileEntry } from "../services/prefs/directorySort";
import type { FilePayload } from "../services/file";
import type { GitDiffPayload, GitStatusPayload } from "../services/git";
import type { QueuedUserMessage, RelatedFile, RelatedWorktree, Session, TokenUsage } from "../services/session";
import type { TaskDetail } from "../services/tasks";
import type { UpdateState } from "../services/update";
import type { ManagedRootPayload } from "./appMisc";
import type { Exchange, MultiProjectSessionGroup, PendingSend, SessionItem, SessionQueueItem, SlashCommandResult, WSStatus } from "./appSession";
import type { KanbanTask } from "../services/tasks";
import { dropTransientExchanges, isSamePendingEcho } from "../services/session";
import { reportError } from "../services/net/error";
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
      taskDetailsByIdRef,
      workspaceOpenRef,
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
      setTaskSessionErrorDialog,
    },
    actions: {
      applyManagedRootRename,
      applyTaskDetails,
      bumpCacheVersion,
      loadManagedRootPayloads,
      loadSessionsForRoot,
      markSessionStale,
      playCompletionSound,
      promotePendingSessionForRoot,
      pruneTaskDetails,
      refreshManagedRoots,
      refreshMultiProjectReplyingSessions,
      refreshTaskWorktree,
      refreshTasksForRelatedSession,
      refreshWorkspaceBoard,
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
            playCompletionSound();
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
      "task.updated": (event: any, payload: any) => {
          // 工作台是跨项目视图：它要显示所有项目的任务，只听 currentRootId 会让
          // 「在别处把任务点完成，工作台卡片不变」——taskDetailsById 不动，
          // 依赖它的整包重拉 effect 也不触发。板态仍只收当前项目，免得污染项目看板。
          if (
            typeof payload?.root_id === "string" &&
            (workspaceOpenRef.current || payload.root_id === currentRootIdRef.current) &&
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
      "task.finish_teardown": (event: any, payload: any) => {
          // 收尾段成功之后，服务端在**自己的** goroutine 里做清场（拆目录 → 删分支
          // → 搬会话）。那条路没有任何 HTTP 响应会回来，界面唯一能知道结果的
          // 就是这一条推送 —— 不接的话收尾就变成「点了没反应，徽标悄悄变了」。
          if (typeof payload?.root_id !== "string" || typeof payload?.task_id !== "string") {
            return;
          }
          // 清场会清空 worktree_path，任务因此可能不该再挂在工作台的任何筛选下 ——
          // 那是结构变化，只能靠重拉扇出看得见（task.updated 只换卡上的内容）。
          if (workspaceOpenRef.current) {
            refreshWorkspaceBoard();
          }
          const conflicts: string[] = Array.isArray(payload.conflict_files)
            ? payload.conflict_files.map(String).filter(Boolean)
            : [];
          const dirtyFiles: string[] = Array.isArray(payload.dirty_files)
            ? payload.dirty_files.map(String).filter(Boolean)
            : [];
          const userChanges: string[] = Array.isArray(payload.user_changes)
            ? payload.user_changes.map(String).filter(Boolean)
            : [];
          const lines: string[] = [];
          const result = payload.result;
          if (result?.commit) lines.push(String(result.commit));
          if (result?.worktree_removed) lines.push(t("task.finishWorktreeRemoved"));
          // 目录本体（含 git 不跟踪的 .mindfs/ .omc/ .claude/）也删掉了 —— 这是收尾
          // 「这个目录不再需要」的落地证据，和「git 拆了 worktree」是两件事。
          if (result?.worktree_dir_removed) lines.push(t("task.finishWorktreeDirRemoved"));
          if (result?.branch_deleted) lines.push(t("task.finishWorktreeBranchDeleted"));
          if (result?.branch_skip_reason) lines.push(String(result.branch_skip_reason));
          if (result?.dir_remove_reason) lines.push(t("task.finishWorktreeDirKept", { reason: String(result.dir_remove_reason) }));
          for (const orphan of (result?.orphans || []) as Array<{ path: string; files?: string[] }>) {
            lines.push(t("task.finishWorktreeOrphan", { path: orphan.path }));
          }
          // 顺手清掉的空壳（只剩工具状态目录）和搬回来的附件都要报出来 ——
          // 「它删了什么 / 搬了什么」不报，用户只会觉得东西凭空消失了。
          for (const removed of (result?.removed_orphans || []) as string[]) {
            lines.push(t("task.finishWorktreeOrphanRemoved", { path: removed }));
          }
          const migrated = (result?.migrated_uploads || []) as string[];
          if (migrated.length > 0) {
            lines.push(t("task.finishWorktreeUploadsMoved", { files: migrated.join(", ") }));
          }
          for (const change of userChanges) lines.push(change);
          const note = String(payload.note || "").trim();
          if (note) lines.push(note);
          const sessionWarning = String(payload.session_warning || "").trim();
          if (sessionWarning) lines.push(sessionWarning);

          if (payload.error) {
            // 合并撞上冲突：仓库停在 MERGE_HEAD，worktree 和分支都还在、代码没丢，
            // 但要人工解 —— 列出文件，别只丢一句「合并失败」。
            if (conflicts.length > 0) {
              setTaskSessionErrorDialog({
                title: t("task.finishWorktreeConflict"),
                message: String(payload.error),
                details: conflicts,
              });
            } else if (dirtyFiles.length > 0) {
              // 主 checkout 有未提交改动：那不是「收尾炸了」，是「你自己的活还没收」。
              // 同样要列文件 —— 用户得知道是哪几个，才能去提交或暂存。
              setTaskSessionErrorDialog({
                title: t("task.finishWorktreeDirty"),
                message: t("task.finishWorktreeDirtyHint"),
                details: dirtyFiles,
              });
            } else if (userChanges.length > 0) {
              // worktree 里还有没提交的东西：合并**已经成功了**，只是目录拆不掉。
              // 这条最容易让人误判成「白干了一场」—— 所以文案必须点明「合并已成功」。
              setTaskSessionErrorDialog({
                title: t("task.finishWorktreeUserChanges"),
                message: t("task.finishWorktreeUserChangesHint"),
                details: userChanges,
              });
            } else {
              reportError("task.action_failed", String(payload.error), { severity: "error", recoverable: true });
            }
            return;
          }
          // 成功：info 档（accent 色、自动消失）而不是红的错误色 —— 这是个好消息，
          // 红色会让用户以为收尾炸了，反而不敢去动那个已经拆掉的目录。
          reportError("task.action_failed", lines.length > 0 ? lines.join(" / ") : t("task.finishWorktreeDone"), {
            severity: "info",
            recoverable: false,
          });
      },
      "task.deleted": (event: any, payload: any) => {
          // 后端一直在发（appcontext.go 的 TaskDeleted），前端却从来没接过：
          // 删掉的任务会一直挂在看板和工作台上，直到用户手动重拉一次。
          const taskId = String(payload?.task_id || "").trim();
          const rootId = String(payload?.root_id || "").trim();
          if (!taskId || !rootId) {
            return;
          }
          // 摘内存：keep 集合 = 现有全部 id 减去这一个。pruneTaskDetails 内部按 rootId
          // 划定作用域，别的项目不会被误伤。选中态不用管 —— 摘掉之后
          // selectedKanbanTask 解析成 null、面板自己关掉，既有守卫再把 id 清干净。
          pruneTaskDetails(
            rootId,
            Object.keys(taskDetailsByIdRef.current).filter((id) => id !== taskId),
          );
          // 卡片「还在不在」是结构问题，getLiveTask 那条就地更新路径看不见它。
          if (workspaceOpenRef.current) {
            refreshWorkspaceBoard();
          }
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
    ...createSessionMetaHandlers(ctx, local),
  };
}
