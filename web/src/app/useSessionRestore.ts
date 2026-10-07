// 从 App.tsx 抽出的 useEffect（机械搬移，effect 体逐字未改）。
import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { clearStalePending, hasSessionExchanges, isSessionShownInMain, isTopLevelSessionItem, mergeReplyingStateByNode, normalizeMode, relatedFileSelectionKey, sessionInputHistory, shouldAutoSelectNewSession, toSessionItem } from "./appSession";
import type { Dispatch, SetStateAction, RefObject } from "react";
import { SESSION_WINDOW_SIZE, clearCachedSessionsForRoot, clearWindowedView, composeLoadedExchanges, deleteCachedSession, deleteCachedSessionLists, getCachedMultiRootSessionList, getCachedSession, getCachedSessionList, getSessionWindow, isTransientExchange, saveCachedMultiRootSessionList, saveCachedSessionList, sessionService, setCachedSessionRelatedFiles, setWindowedView, settlePendingAcks, syncSession, type MultiRootSessionGroup, type QueuedUserMessage, type RelatedFile, type RelatedWorktree, type Session, type TokenUsage } from "../services/session";
import { AttachedFileContext, Exchange, GitFileStat, MultiProjectSessionGroup, PendingSend, RelatedFileClickTarget, SessionItem, SessionMode, SessionQueueItem, SlashCommandResult, ViewerSelection, WSStatus } from "./appSession";
import type { SyncSessionResult } from "../services/session";

export interface useSessionRestoreParams {
  boundSessionByRootRef: RefObject<Record<string, string | null>>;
  bumpCacheVersion: () => void;
  clearSessionStale: (rootID: string | null | undefined, sessionKey: string | null | undefined) => void;
  currentRootId: string | null;
  currentRootIdRef: RefObject<string | null>;
  hasSessionExchanges: typeof hasSessionExchanges;
  isSessionStale: (rootID: string | null | undefined, sessionKey: string | null | undefined) => boolean;
  loadRootID: string;
  loadSessionKey: string;
  loadSnapshotHasExchanges: boolean;
  loadedSessionRef: RefObject<Record<string, boolean>>;
  loadingSessionRef: RefObject<Partial<Record<string, Promise<SyncSessionResult>>>>;
  resolvePendingForSession: (rootID: string | null | undefined, sessionKey: string | null | undefined, fallback?: boolean | undefined) => boolean;
  restoreActiveSession: (rootID: string | null | undefined, sessionKey: string | null | undefined) => Promise<Session | null>;
  rootSessionKey: (rootId: string, sessionKey: string) => string;
  scopedRootKey: (rootId: string) => string;
  selectedSessionLoading: boolean;
  sessionCacheRef: RefObject<Record<string, Session>>;
  sessionLoadFailureRef: RefObject<Record<string, { at: number; status: number; }>>;
  setDrawerSessionForRoot: (rootID: string | null | undefined, session: SessionItem | Session | null) => void;
  setSelectedSession: Dispatch<SetStateAction<SessionItem | null>>;
  toSessionItem: typeof toSessionItem;
  useEffect: typeof useEffect;
}

export function useSessionRestore(params: useSessionRestoreParams) {
  const boundSessionByRootRef = params.boundSessionByRootRef;
  const bumpCacheVersion = params.bumpCacheVersion;
  const clearSessionStale = params.clearSessionStale;
  const currentRootId = params.currentRootId;
  const currentRootIdRef = params.currentRootIdRef;
  const hasSessionExchanges = params.hasSessionExchanges;
  const isSessionStale = params.isSessionStale;
  const loadRootID = params.loadRootID;
  const loadSessionKey = params.loadSessionKey;
  const loadSnapshotHasExchanges = params.loadSnapshotHasExchanges;
  const loadedSessionRef = params.loadedSessionRef;
  const loadingSessionRef = params.loadingSessionRef;
  const resolvePendingForSession = params.resolvePendingForSession;
  const restoreActiveSession = params.restoreActiveSession;
  const rootSessionKey = params.rootSessionKey;
  const scopedRootKey = params.scopedRootKey;
  const selectedSessionLoading = params.selectedSessionLoading;
  const sessionCacheRef = params.sessionCacheRef;
  const sessionLoadFailureRef = params.sessionLoadFailureRef;
  const setDrawerSessionForRoot = params.setDrawerSessionForRoot;
  const setSelectedSession = params.setSelectedSession;
  const toSessionItem = params.toSessionItem;
  const useEffect = params.useEffect;

useEffect(() => {
    const sessionKey = loadSessionKey;
    const rootID = loadRootID;
    if (!rootID || !sessionKey || sessionKey.startsWith("pending-")) {
      return;
    }
    if (selectedSessionLoading) {
      return;
    }
    const cacheKey = rootSessionKey(rootID, sessionKey);
    const isStale = isSessionStale(rootID, sessionKey);
    const cached = sessionCacheRef.current[cacheKey];
    if (!isStale && loadedSessionRef.current[cacheKey]) {
      return;
    }
    if (!isStale && hasSessionExchanges(cached)) {
      return;
    }
    if (!isStale && loadSnapshotHasExchanges) {
      return;
    }
    if (loadingSessionRef.current[cacheKey]) {
      return;
    }
    // CUSTOM(G-AU): 失败冷却。404 永久（会话不存在，重试无意义），其余错误 5s。
    // 这是第二道保险：即便将来又出现「依赖每轮都变」的加载路径，风暴也被压到
    // 每分钟最多一次，而不是每秒 22 次。
    const failure = sessionLoadFailureRef.current[cacheKey];
    if (failure) {
      const cooldown = failure.status === 404 ? Number.POSITIVE_INFINITY : 5_000;
      if (Date.now() - failure.at < cooldown) {
        return;
      }
    }
    void restoreActiveSession(rootID, sessionKey).then((restored) => {
      if (!restored) {
        return;
      }
      loadedSessionRef.current[cacheKey] = true;
      clearSessionStale(rootID, sessionKey);
      delete sessionLoadFailureRef.current[cacheKey];
      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        const prevRoot =
          (prev?.root_id as string | undefined) || currentRootIdRef.current;
        if (!prev || prevKey !== sessionKey || prevRoot !== rootID) {
          return prev;
        }
        return toSessionItem(rootID, {
          ...(prev as any),
          ...(restored as any),
          key: sessionKey,
          session_key: sessionKey,
          root_id: rootID,
        });
      });
      if (boundSessionByRootRef.current[scopedRootKey(rootID)] === sessionKey) {
        setDrawerSessionForRoot(rootID, restored);
      }
    });
  }, [
    loadSessionKey,
    loadRootID,
    loadSnapshotHasExchanges,
    selectedSessionLoading,
    currentRootId,
    rootSessionKey,
    bumpCacheVersion,
    clearSessionStale,
    isSessionStale,
    resolvePendingForSession,
    restoreActiveSession,
    setDrawerSessionForRoot,
  ])
}
