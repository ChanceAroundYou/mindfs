import { useCallback } from "react";
import { waitForNextPaint } from "./appMisc";
import { buildDirectorySelectionKey } from "./appPath";
import { toSessionItem, type SessionItem } from "./appSession";
import type { UseSessionLifecycleParams } from "./useSessionLifecycleTypes";
import { getCachedSession } from "../services/session";
import { unreadCompletedRemove, unreadKey } from "../shared/unreadCompleted";
import type { Session } from "../services/session";

export type UseSessionSelectionParams = UseSessionLifecycleParams & {
  isMobile: boolean;
  restoreActiveSession: (
    rootID: string | null | undefined,
    sessionKey: string | null | undefined,
  ) => Promise<Session | null>;
};

export function useSessionSelection(params: UseSessionSelectionParams) {
  const {
    boundSessionByRootRef,
    bumpCacheVersion,
    clearSessionStale,
    currentRootIdRef,
    currentRootNodeIdRef,
    drawerSessionByRootRef,
    fileCursorRef,
    getNodeIdForRoot,
    isMobile,
    isSessionStale,
    loadedSessionRef,
    mainViewRef,
    resolvePendingForSession,
    restoreActiveSession,
    rootSessionKey,
    scopedRootKey,
    selectedSessionByRootRef,
    sessionCacheRef,
    sessionSearchTargetCounterRef,
    setCurrentRootId,
    setBoundSessionForRoot,
    setDrawerOpenForRoot,
    setDrawerSessionForRoot,
    setFile,
    setGitDiff,
    setInteractionMode,
    setIsLeftOpen,
    setIsRightOpen,
    setMainEntries,
    replaceURLState,
    setSelectedDir,
    setSelectedDirKey,
    setSelectedKanbanTaskId,
    setSelectedSession,
    setSelectedSessionLoading,
    setPluginQuery,
    selectRootNode,
    switchMainView,
  } = params;

  const handleSelectSession = useCallback(
    async (
      session: any,
      options?: { preserveTaskSelection?: boolean; preserveMainView?: boolean },
    ) => {
      const key = session?.key || session?.session_key;
      const targetRoot =
        (session?.root_id as string | undefined) || currentRootIdRef.current;
      if (!targetRoot || !key) return;
      if (!options?.preserveTaskSelection) {
        setSelectedKanbanTaskId("");
      }
      // 多节点同名项目：会话归属节点优先于当前选中节点（跨节点切换会清理旧会话态）
      const sessionNode = String((session as any)?._nodeId || "").trim();
      const nextNode =
        sessionNode ||
        (targetRoot === String(currentRootIdRef.current || "")
          ? String(currentRootNodeIdRef.current || "")
          : "") ||
        getNodeIdForRoot(targetRoot) ||
        "";
      if (
        targetRoot !== String(currentRootIdRef.current || "") ||
        (sessionNode && sessionNode !== String(currentRootNodeIdRef.current || ""))
      ) {
        setCurrentRootId(targetRoot);
        selectRootNode(targetRoot, nextNode || undefined);
      }
      // 看过了就熄灭「完成未读」：所有选中路径（列表点击、深链恢复、看板跳转）都汇聚到这里，
      // 只在列表点击处清会让「从看板/通知点进来」的那条永远亮着。
      unreadCompletedRemove(unreadKey(nextNode, targetRoot, key));
      setBoundSessionForRoot(targetRoot, key);
      setDrawerSessionForRoot(targetRoot, toSessionItem(targetRoot, session));
      setSelectedDir(targetRoot);
      setSelectedDirKey(
        buildDirectorySelectionKey(getNodeIdForRoot(targetRoot), targetRoot, targetRoot, true),
      );
      // 深链恢复时主面板模式以 URL 的 view 为准，不因为「有 session」就把面板硬拉回对话态。
      if (!options?.preserveMainView) {
        switchMainView("chat");
      }
      // 首帧 pending 只认真相表，不认抽屉/列表项上的陈旧快照 —— 切会话时那两份
      // 可能还停在「上一轮在跑」上，先亮后灭就是可见的闪。查不到就按不在跑。
      const preservePending = resolvePendingForSession(targetRoot, key, false);
      const searchTargetId =
        typeof session?.search_seq === "number"
          ? `${key}:${session.search_seq}:${++sessionSearchTargetCounterRef.current}`
          : undefined;
      replaceURLState({
        root: targetRoot,
        file: "",
        session: key,
        cursor: 0,
        pluginQuery: {},
      });
      selectedSessionByRootRef.current[scopedRootKey(targetRoot)] = key;
      const cacheKey = rootSessionKey(targetRoot, key);
      setSelectedSessionLoading(true);
      setSelectedSession(
        toSessionItem(targetRoot, {
          ...(session as any),
          pending: preservePending,
          search_target_id: searchTargetId || session?.search_target_id,
        }),
      );
      setInteractionMode("main");
      setDrawerOpenForRoot(targetRoot, false);
      if (isMobile) setIsRightOpen(false);
      await waitForNextPaint();
      const applySession = (
        fullSession: Session,
        options?: { writeCache?: boolean },
      ) => {
        const shouldWriteCache = options?.writeCache !== false;
        const serverPending =
          typeof (fullSession as any)?.pending === "boolean"
            ? !!(fullSession as any).pending
            : undefined;
        const pending =
          serverPending !== undefined
            ? serverPending
            : resolvePendingForSession(targetRoot, key, preservePending);
        const normalized = {
          ...(fullSession as any),
          key,
          // CUSTOM(G-Z): 所有走 handleSelectSession 的路径写缓存都带 root，
          // 这是「无主缓存」的收口 —— 缺了它，二次跳转会把它当成当前根的会话。
          root_id: targetRoot,
          pending,
        } as Session;
        if (shouldWriteCache) {
          sessionCacheRef.current[cacheKey] = normalized;
        }
        setSelectedSession((prev) => {
          const prevKey = prev?.key || prev?.session_key;
          const prevRoot =
            (prev?.root_id as string | undefined) || currentRootIdRef.current;
          if (prevKey !== key || prevRoot !== targetRoot) {
            return prev;
          }
          return toSessionItem(targetRoot, {
            ...(prev as any),
            ...(normalized as any),
            key,
            session_key: key,
            root_id: targetRoot,
          });
        });
        if ((boundSessionByRootRef.current[scopedRootKey(targetRoot)] || null) === key) {
          setDrawerSessionForRoot(targetRoot, {
            ...(normalized as any),
          } as Session);
        }
        setSelectedSessionLoading(false);
        if (shouldWriteCache) {
          bumpCacheVersion();
        }
      };
      // 先即时渲染本地缓存（快），再始终同步到最新：
      // 移除原"已缓存/已加载就跳过 sync"的短路，避免陈旧缓存导致历史加载/更新滞后（#sync-lag）。
      const cached = sessionCacheRef.current[cacheKey];
      if (cached) {
        applySession(cached);
      } else {
        const persisted = await getCachedSession(targetRoot, key, getNodeIdForRoot(targetRoot));
        if (persisted) {
          applySession(persisted);
        }
      }
      try {
        const restored = await restoreActiveSession(targetRoot, key);
        if (restored) {
          applySession(restored, { writeCache: false });
          loadedSessionRef.current[cacheKey] = true;
          clearSessionStale(targetRoot, key);
        } else {
          setSelectedSessionLoading(false);
        }
      } catch (err) {
        setSelectedSessionLoading(false);
      }
    },
    [
      isMobile,
      rootSessionKey,
      bumpCacheVersion,
      clearSessionStale,
      isSessionStale,
      resolvePendingForSession,
      restoreActiveSession,
      setDrawerOpenForRoot,
      setDrawerSessionForRoot,
      switchMainView,
      replaceURLState,
    ],
  );
  const tryShowBoundSessionForRoot = useCallback(
    async (
      rootID: string | null | undefined,
      options?: {
        pluginQuery?: Record<string, string>;
        closeLeftSidebar?: boolean;
      },
    ): Promise<boolean> => {
      const resolvedRoot = String(rootID || "");
      if (!resolvedRoot) {
        return false;
      }
      if (mainViewRef.current !== "chat") {
        return false;
      }
      const selectedKey = String(
        selectedSessionByRootRef.current[scopedRootKey(resolvedRoot)] || "",
      ).trim();
      const boundKey = String(
        boundSessionByRootRef.current[scopedRootKey(resolvedRoot)] || "",
      ).trim();
      const drawerKey = String(
        drawerSessionByRootRef.current[scopedRootKey(resolvedRoot)]?.key || "",
      ).trim();
      const preferredKey =
        (selectedKey && !selectedKey.startsWith("pending-") ? selectedKey : "") ||
        (boundKey && !boundKey.startsWith("pending-") ? boundKey : "") ||
        (drawerKey && !drawerKey.startsWith("pending-") ? drawerKey : "");
      if (!preferredKey) {
        return false;
      }
      if (currentRootIdRef.current !== resolvedRoot) {
        setCurrentRootId(resolvedRoot);
      }
      setGitDiff(null);
      setFile(null);
      setMainEntries([]);
      setPluginQuery(options?.pluginQuery || {});
      setSelectedDir(resolvedRoot);
      setSelectedDirKey(
        buildDirectorySelectionKey(getNodeIdForRoot(resolvedRoot), resolvedRoot, resolvedRoot, true),
      );
      fileCursorRef.current = 0;
      const cacheKey = rootSessionKey(resolvedRoot, preferredKey);
      let initialSession =
        sessionCacheRef.current[cacheKey] ||
        (await getCachedSession(resolvedRoot, preferredKey, getNodeIdForRoot(resolvedRoot)));
      if (initialSession) {
        sessionCacheRef.current[cacheKey] = {
          ...(initialSession as any),
          key: preferredKey,
        } as Session;
      }
      await handleSelectSession(
        initialSession
          ? ({
              ...(initialSession as any),
              key: preferredKey,
              session_key: preferredKey,
              root_id: resolvedRoot,
            } as SessionItem)
          : {
              key: preferredKey,
              session_key: preferredKey,
              root_id: resolvedRoot,
            },
      );
      if (options?.closeLeftSidebar && isMobile) {
        setIsLeftOpen(false);
      }
      return true;
    },
    [handleSelectSession, isMobile, rootSessionKey, bumpCacheVersion],
  );

  return { handleSelectSession, tryShowBoundSessionForRoot };
}
