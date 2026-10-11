// 从 App.tsx 抽出的 useEffect（机械搬移，effect 体逐字未改）。
import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { SESSION_WINDOW_SIZE, clearCachedSessionsForRoot, clearWindowedView, composeLoadedExchanges, deleteCachedSession, deleteCachedSessionLists, getCachedMultiRootSessionList, getCachedSession, getCachedSessionList, getSessionWindow, isTransientExchange, saveCachedMultiRootSessionList, saveCachedSessionList, sessionService, setCachedSessionRelatedFiles, setWindowedView, settlePendingAcks, syncSession, type MultiRootSessionGroup, type QueuedUserMessage, type RelatedFile, type RelatedWorktree, type Session, type TokenUsage } from "../services/session";
import { APP_DOCUMENT_TITLE, AppProps, CHILD_SESSION_PAGE_SIZE, MULTI_PROJECT_SESSION_LIMIT, ManagedRootPayload, SESSION_PAGE_SIZE } from "./appMisc";
import { basenameOfPath, buildDirectorySelectionKey, buildFileScrollKey, buildURLSearch, comparableManagedRootPath, dirnameOfPath, isDirectorySortMode, joinDisplayPath, normalizeCursor, normalizePath, parentDirsOfFile, parseFileLocation, parsePluginQuery, readURLState, relativeDisplayPathFromRoot, rootNodeKey } from "./appPath";
import { hasSessionExchanges, isSessionShownInMain, isTopLevelSessionItem, mergeReplyingStateByNode, normalizeMode, relatedFileSelectionKey, sessionInputHistory, shouldAutoSelectNewSession, toSessionItem } from "./appSession";
import type { Dispatch, SetStateAction, RefObject } from "react";
import { AttachedFileContext, Exchange, GitFileStat, MultiProjectSessionGroup, PendingSend, RelatedFileClickTarget, SessionItem, SessionMode, SessionQueueItem, SlashCommandResult, ViewerSelection, WSStatus } from "./appSession";

export interface useArchiveSyncParams {
  SESSION_PAGE_SIZE: typeof SESSION_PAGE_SIZE;
  archiveOpen: boolean;
  archiveReloadToken: number;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  managedRootByKeyRef: RefObject<Record<string, ManagedRootPayload>>;
  managedRootIdsRef: RefObject<Set<string>>;
  rootNodeKey: typeof rootNodeKey;
  sessionService: typeof sessionService;
  setArchiveLoading: Dispatch<SetStateAction<boolean>>;
  setArchivedGroups: Dispatch<SetStateAction<MultiProjectSessionGroup[]>>;
  toSessionItem: typeof toSessionItem;
  useEffect: typeof useEffect;
}

export function useArchiveSync(params: useArchiveSyncParams) {
  const SESSION_PAGE_SIZE = params.SESSION_PAGE_SIZE;
  const archiveOpen = params.archiveOpen;
  const archiveReloadToken = params.archiveReloadToken;
  const getNodeIdForRoot = params.getNodeIdForRoot;
  const managedRootByKeyRef = params.managedRootByKeyRef;
  const managedRootIdsRef = params.managedRootIdsRef;
  const rootNodeKey = params.rootNodeKey;
  const sessionService = params.sessionService;
  const setArchiveLoading = params.setArchiveLoading;
  const setArchivedGroups = params.setArchivedGroups;
  const toSessionItem = params.toSessionItem;
  const useEffect = params.useEffect;

  // 归档视图懒加载：只有打开过 / 归档状态变过才拉，未打开时零成本。
  // topLevel 是硬要求：归档区按项目平铺，子会话挂在那儿只会变成孤儿行。
  // 按**全部**已加载项目查，而不是按多项目分组里出现过的 rootId ——
  // 某个项目的会话被**全部**归档后该分组就不再出现，按分组查会漏掉它的归档行
  // （正是「该项目的对话全归档了就再也没法取消归档」的那个 bug）。
  useEffect(() => {
      if (!archiveOpen) return;
      const rootIds = Array.from(managedRootIdsRef.current)
        .map((id) => String(id || "").trim())
        .filter(Boolean);
      if (rootIds.length === 0) return;
      let cancelled = false;
      setArchiveLoading(true);
      (async () => {
        try {
          const payloads = await Promise.all(
            rootIds.map((rootId) =>
              sessionService.fetchSessions(rootId, {
                nodeId: getNodeIdForRoot(rootId),
                limit: SESSION_PAGE_SIZE,
                archivedOnly: true,
                topLevel: true,
              }),
            ),
          );
          if (cancelled) return;
          const groups = rootIds
            .map((rootId, i) => {
              const payload = payloads[i];
              const sessions = payload.items
                .map((item) =>
                  toSessionItem(rootId, {
                    ...(item as any),
                    root_id: rootId,
                    _nodeId: getNodeIdForRoot(rootId),
                  }),
                )
                .filter((item): item is SessionItem => !!item)
                .sort(
                  (a, b) =>
                    String(b.updated_at || "").localeCompare(String(a.updated_at || "")) ||
                    String(a.key).localeCompare(String(b.key)),
                );
              if (sessions.length === 0) return null;
              const nodeId = getNodeIdForRoot(rootId);
              const meta = (managedRootByKeyRef.current as Record<string, any>)[
                rootNodeKey(nodeId, rootId)
              ];
              return {
                rootId,
                rootName: meta?.display_name || rootId,
                latestSessionTime: String(sessions[0]?.updated_at || ""),
                _nodeId: nodeId || undefined,
                _nodeColor: meta?._nodeColor,
                sessions,
                totalCount: payload.totalCount || sessions.length,
              } as MultiProjectSessionGroup;
            })
            .filter((g): g is MultiProjectSessionGroup => !!g);
          setArchivedGroups(groups);
        } finally {
          if (!cancelled) setArchiveLoading(false);
        }
      })();
      return () => {
        cancelled = true;
      };
    }, [archiveOpen, archiveReloadToken])
}
