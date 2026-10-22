/**
 * 会话生命周期协调器：恢复、选择和重命名留在本模块；列表加载与手动同步分别委托给同域 hook。
 * `UseSessionLifecycleParams` 是 App session core 提供的跨 hook 契约。
 */
import { useCallback, useMemo, type MutableRefObject, type Dispatch, type SetStateAction } from "react";
import { CHILD_SESSION_PAGE_SIZE, useResponsive, waitForNextPaint } from "./appMisc";
import { MainViewMode, URLState, buildDirectorySelectionKey } from "./appPath";
import { Exchange, MultiProjectSessionGroup, SessionItem, WSStatus, toSessionItem } from "./appSession";
import { useActionHandlers } from "./useActionHandlers";
import { useSessionSyncAction } from "./useSessionSyncAction";
import { useSessionListLoading } from "./useSessionListLoading";
import { useCompletionSound } from "./useCompletionSound";
import { useExternalSessionImport } from "./useExternalSessionImport";
import { useGitActions } from "./useGitActions";
import { useGitData } from "./useGitData";
import { useProjectLifecycle } from "./useProjectLifecycle";
import { useProjectTreeWorktrees } from "./useProjectTreeWorktrees";
import { useSendMessage } from "./useSendMessage";
import { useSessionCache } from "./useSessionCache";
import { useSessionSearch } from "./useSessionSearch";
import { useSessionSidebarView } from "./useSessionSidebarView";
import { useSessionSelection } from "./useSessionSelection";
import { useSessionStreamCache } from "./useSessionStreamCache";
import { useTaskTemplates } from "./useTaskTemplates";
import { useI18n } from "../i18n";
import { type FilePayload } from "../services/file";
import type { UseSessionLifecycleParams } from "./useSessionLifecycleTypes";
import { type GitDiffPayload } from "../services/git";
import { reportError } from "../services/net/error";
import { type FileEntry } from "../services/prefs/directorySort";
import { type Session, OPEN_SESSION_WINDOW_REUSE_MS, SESSION_WINDOW_SIZE, clearWindowedView, composeLoadedExchanges, getCachedSession, getCachedSessionList, getSessionWindow, saveCachedSessionList, sessionService, setWindowedView, syncSession } from "../services/session";
import { mergeSessionItems } from "../services/session/sessionListMerge";

export function useSessionLifecycle(params: UseSessionLifecycleParams) {
  const { t } = useI18n();
  const { isMobile } = useResponsive();
  const {
    anchorSeqRef,
    applyPendingToMultiProjectGroups,
    applyPinSnapshot,
    boundSessionByRootRef,
    bumpCacheVersion,
    clearLocalPendingForSession,
    clearSessionStale,
    currentRootId,
    currentRootIdRef,
    currentRootNodeIdRef,
    drawerSessionByRootRef,
    enforceSessionCacheBound,
    file,
    fileCursorRef,
    getNodeIdForRoot,
    isSessionStale,
    listLoadErrorAtRef,
    loadedSessionRef,
    loadingSessionRef,
    mainViewRef,
    multiProjectPendingRef,
    pluginQuery,
    replaceURLState,
    resolvePendingForSession,
    rootSessionKey,
    scopedRootKey,
    selectRootNode,
    selectedSessionByRootRef,
    selectedSessionRef,
    sessionCacheAccessRef,
    sessionCacheRef,
    sessionListAbortRef,
    sessionListLoadSeqRef,
    sessionLoadFailureRef,
    sessionSearchTargetCounterRef,
    sessions,
    setBoundSessionForRoot,
    setCurrentRootId,
    setDrawerOpenForRoot,
    setDrawerSessionForRoot,
    setFile,
    setGitDiff,
    setHasMoreSessions,
    setInteractionMode,
    setIsLeftOpen,
    setIsRightOpen,
    setMainEntries,
    setMultiProjectSessionGroups,
    setPluginQuery,
    setSelectedDir,
    setSelectedDirKey,
    setSelectedKanbanTaskId,
    setSelectedSession,
    setSelectedSessionLoading,
    setSessions,
    status,
    switchMainView,
  } = params;

  const restoreActiveSession = useCallback(
    async (
      rootID: string | null | undefined,
      sessionKey: string | null | undefined,
    ): Promise<Session | null> => {
      const resolvedRoot = String(rootID || "");
      const resolvedKey = String(sessionKey || "");
      if (!resolvedRoot || !resolvedKey || resolvedKey.startsWith("pending-")) {
        return null;
      }
      const cacheKey = rootSessionKey(resolvedRoot, resolvedKey);
      const cachedBeforeSync = sessionCacheRef.current[cacheKey];
      // 加载前内存缓存里的行 —— 「在途内容（seq=0）」的唯一来源。
      // 服务端不下发它们（窗口回包只有已落盘行），所以每次加载都必须从这份快照接回，
      // 见 session.ts 的 composeLoadedExchanges。
      const cachedExchangesBefore: Exchange[] = Array.isArray(
        (cachedBeforeSync as any)?.exchanges,
      )
        ? ((cachedBeforeSync as any).exchanges as Exchange[])
        : [];
      // 这里**不需要**任何「事件进度」的簿记了（2026-10-06）。
      //
      // 曾经此处有一段 `clearEventCursor`，以及一段论证它「不能清」的注释 —— 那都是
      // 「客户端自报进度、服务端只补发缺的那几条」那套续传协议的残余。协议本身已删：
      // 挂载会话一律是**快照重建**（服务端回一整批带 `reset:true` 的事件，客户端先清
      // 瞬时尾巴再照单应用），所以「我收到哪儿了」不再是任何一方的输入。
      //
      // 顺序上这里也没问题：本函数在窗口/sync 结果**并入缓存之后**才调 markSessionReady
      // （下面 :2337 / :2396），所以重置帧总是落在组装好的缓存之上，而不是被它覆盖。
      const inflight = loadingSessionRef.current[cacheKey] as unknown as Promise<Session | null> | undefined;
      if (inflight) {
        const hit = await inflight;
        return hit;
      }
      const promise = (async (): Promise<Session | null> => {
        // 方案 B 首屏按需：先尝试窗口化拉取最新 50 条，失败回退全量 syncSession
        try {
          setWindowedView(resolvedKey, true);
          const win = await getSessionWindow(resolvedRoot, resolvedKey, {
            latest: SESSION_WINDOW_SIZE,
            nodeId: getNodeIdForRoot(resolvedRoot),
            // CUSTOM(G-JU): 紧接着挂载的 SessionViewer init effect 会拉同参数窗口；
            // 这里把结果留 2 秒可复用，让「打开会话」这条路只发一次请求。
            reuseWithinMs: OPEN_SESSION_WINDOW_REUSE_MS,
          });
          if (win && (win as any).session) {
            const sess: any = {
              ...(win as any).session,
              key: resolvedKey,
              session_key: resolvedKey,
            };
            const serverPending =
              typeof sess?.pending === "boolean" ? !!sess.pending : undefined;
            if (serverPending === false) {
              clearLocalPendingForSession(resolvedRoot, resolvedKey);
            }
            const pending =
              serverPending === false
                ? false
                : resolvePendingForSession(resolvedRoot, resolvedKey, !!serverPending);
            const anchorAt = ++anchorSeqRef.current;
            // 窗口**并入**缓存，不是**替换**缓存。替换就是「切会话闪一下」的
            // 第二来源（2026-10-05）：窗口只有尾部 SESSION_WINDOW_SIZE 条，
            // 而缓存里可能有几百条（用户翻上去了、或 live 路径落库过）。
            // 整体覆盖会把头部历史当场扔掉，切回来时首帧种子只剩 20 条 ——
            // 于是「完整对话 → 塌成最后一条用户消息 → 慢慢补回来」。
            // 淘汰只在**写入**时发生、且只从头部淘汰（见 toPersistentSession）；
            // 加载过程中一律只增不减。
            const winExs = Array.isArray(sess.exchanges)
              ? (sess.exchanges as Exchange[])
              : [];
            const toCache = {
              ...sess,
              key: resolvedKey,
              // CUSTOM(G-Z): 窗口/列表回包不带 root_id（root 只是请求参数），
              // 这里不补，缓存里就是「无主」会话 —— 二次跳转时归属会退化成当前根。
              root_id: resolvedRoot,
              pending,
              // 加载时的唯一组装规则：服务端行并入缓存，在途时再把 seq=0 接回。
              // 判据是 `pending` —— 不在跑就没有在途内容，缓存里的 seq=0 全是残留
              // （跨多次 compact 堆起来的那种，渲染出来就是「同一段正文两遍」）。
              exchanges: composeLoadedExchanges(winExs, cachedExchangesBefore, pending),
              // aux 同理并入：exchanges 保住历史而 exchange_aux 只剩窗口那份，
              // 那些行上的工具卡会凭空消失（历史里的 read/edit 变纯文本）。
              // 按 seq 覆盖而非拼接 —— 同一个 seq 被窗口与缓存各带一份时，
              // 拼接会把同一张卡渲染两遍（appendExchangeAuxDelta 是拼接语义，不能用）。
              exchange_aux: {
                ...((cachedBeforeSync as any)?.exchange_aux || {}),
                ...(sess.exchange_aux || {}),
              },
              // 正文块（CUSTOM(G-BH)）与 aux 完全同一条规则，理由也一样：
              // 窗口只带窗口内那部分块，整体替换会让历史轮次的正文当场退回旧格式
              //（S2d 删掉 content 后就是「历史轮次全空」）。按 seq 覆盖而非拼接 ——
              // 同一 seq 被窗口与缓存各带一份时，拼接会让旧 revision 与新 revision 并存。
              blocks: {
                ...((cachedBeforeSync as any)?.blocks || {}),
                ...(sess.blocks || {}),
              },
              _windowMeta: win.meta,
              _anchoredAt: anchorAt,
            } as Session;
            sessionCacheRef.current[cacheKey] = toCache;
            // CUSTOM(G-AU): 新键入缓存时顺手封顶（见 enforceSessionCacheBound）。
            sessionCacheAccessRef.current[cacheKey] = Date.now();
            enforceSessionCacheBound();
            bumpCacheVersion();
            await sessionService.markSessionReady(resolvedRoot, resolvedKey);
            return toCache;
          }
          // 窗口未命中则清标记，走全量回退
          clearWindowedView(resolvedKey);
        } catch {
          clearWindowedView(resolvedKey);
        }
        // 回退：全量 syncSession（保持原有 pending/转瞬态逻辑）
        const syncResult = await syncSession(resolvedRoot, resolvedKey, {
          nodeId: getNodeIdForRoot(resolvedRoot),
        });
        let fullSession = syncResult?.session as any;
        if (!fullSession) {
          return null;
        }
        const serverPending =
          typeof (fullSession as any)?.pending === "boolean"
            ? !!(fullSession as any).pending
            : undefined;
        if (serverPending === false) {
          clearLocalPendingForSession(resolvedRoot, resolvedKey);
        }
        const pending =
          serverPending === false
            ? false
            : resolvePendingForSession(resolvedRoot, resolvedKey, !!serverPending);
        // 与窗口分支同一条组装规则。syncSession 的结果来自 IDB 缓存 + 服务端增量，
        // 它不知道内存里正在流式输出的那部分 —— 必须由这里接回（实测 2026-10-06：
        // 服务端只回 15 条已落盘行，内存缓存有 376 条，其中 361 条是在途内容）。
        // 判据是 `pending`：不在跑就没有在途内容，缓存里的 seq=0 全是残留。
        fullSession = {
          ...fullSession,
          exchanges: composeLoadedExchanges(
            (fullSession as any).exchanges,
            cachedExchangesBefore,
            pending,
          ),
        } as Session;
        const anchorAt = ++anchorSeqRef.current;
        const anchoredExs = Array.isArray((fullSession as any)?.exchanges)
          ? ((fullSession as any).exchanges as any[])
          : [];
        const anchoredMeta = {
          total: anchoredExs.length,
          hasMore: false,
          minSeq: anchoredExs.length ? Number(anchoredExs[0]?.seq || 0) : 0,
          maxSeq: anchoredExs.length
            ? Number(anchoredExs[anchoredExs.length - 1]?.seq || 0)
            : 0,
        };
        sessionCacheRef.current[cacheKey] = {
          ...(fullSession as any),
          key: resolvedKey,
          // CUSTOM(G-Z): 同窗口分支 —— 全量 sync 回包同样不带 root_id。
          root_id: resolvedRoot,
          pending,
          _windowMeta: anchoredMeta as any,
          _anchoredAt: anchorAt,
        } as Session;
        // CUSTOM(G-AU): 新键入缓存时顺手封顶（见 enforceSessionCacheBound）。
        sessionCacheAccessRef.current[cacheKey] = Date.now();
        enforceSessionCacheBound();
        bumpCacheVersion();
        await sessionService.markSessionReady(resolvedRoot, resolvedKey);
        return {
          ...(fullSession as any),
          key: resolvedKey,
          root_id: resolvedRoot,
          pending,
          _windowMeta: anchoredMeta as any,
          _anchoredAt: anchorAt,
        } as Session;
      })();
      loadingSessionRef.current[cacheKey] = promise as any;
      try {
        return await promise;
      } catch (err) {
        // CUSTOM(G-AU): 失败必须留下痕迹。原先异常原样抛出，而调用点只写 .then 不写
        // .catch —— 失败既不改状态也不留记录，和「还没加载」完全等价，下一轮依赖一变
        // 就再拉一次。404 记成永久（会话确实不存在，重试无意义），其余错误退避 5s。
        const status = (err as { status?: number } | null)?.status || 0;
        sessionLoadFailureRef.current[cacheKey] = { at: Date.now(), status };
        return null;
      } finally {
        delete loadingSessionRef.current[cacheKey];
      }
    },
    [
      bumpCacheVersion,
      clearLocalPendingForSession,
      enforceSessionCacheBound,
      resolvePendingForSession,
      rootSessionKey,
    ],
  );
  const { syncSessionHeaderFromListItem, loadSessionsForRoot, loadChildSessionsForParent } = useSessionListLoading(params);
  const { handleSelectSession, tryShowBoundSessionForRoot } = useSessionSelection({
    ...params,
    isMobile,
    restoreActiveSession,
  });
  const handleRenameSession = useCallback(
    async (session: SessionItem, nextName: string) => {
      const sessionKey = session?.key || session?.session_key;
      const rootID =
        (session?.root_id as string | undefined) || currentRootIdRef.current;
      if (!rootID || !sessionKey) return false;

      const trimmedName = nextName.trim();
      if (!trimmedName || trimmedName === String(session?.name || "").trim()) {
        return true;
      }

      const renamed = await sessionService.renameSession(
        rootID,
        sessionKey,
        trimmedName,
        String((session as any)?._nodeId || "") || getNodeIdForRoot(rootID),
      );
      if (!renamed) {
        reportError("session.rename_failed", t("session.renameFailed"));
        return false;
      }

      setSessions((prev) =>
        prev.map((item) =>
          (item.key || item.session_key) === sessionKey
            ? ({
                ...item,
                name: renamed.name,
                updated_at: renamed.updated_at,
              } as SessionItem)
            : item,
        ),
      );

      const cacheKey = rootSessionKey(rootID, sessionKey);
      const cached = sessionCacheRef.current[cacheKey];
      if (cached) {
        sessionCacheRef.current[cacheKey] = {
          ...cached,
          name: renamed.name,
          updated_at: renamed.updated_at,
        } as Session;
      }

      if (
        (selectedSessionRef.current?.key ||
          selectedSessionRef.current?.session_key) === sessionKey
      ) {
        setSelectedSession((prev) =>
          prev
            ? ({
                ...prev,
                name: renamed.name,
                updated_at: renamed.updated_at,
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
      setMultiProjectSessionGroups((prev) =>
        prev.map((group) =>
          group.rootId === rootID && (!(session as any)?._nodeId || (group as any)._nodeId === (session as any)?._nodeId)
            ? {
                ...group,
                sessions: group.sessions.map((item) =>
                  (item.key || item.session_key) === sessionKey
                    ? ({
                        ...item,
                        name: renamed.name,
                        updated_at: renamed.updated_at,
                      } as SessionItem)
                    : item,
                ),
              }
            : group,
        ),
      );

      bumpCacheVersion();
      return true;
    },
    [bumpCacheVersion, rootSessionKey, setDrawerSessionForRoot],
  );
  const { handleSyncSession } = useSessionSyncAction(params);

  return {
    restoreActiveSession,
    syncSessionHeaderFromListItem,
    loadSessionsForRoot,
    loadChildSessionsForParent,
    handleSelectSession,
    tryShowBoundSessionForRoot,
    handleRenameSession,
    handleSyncSession,
  };
}
