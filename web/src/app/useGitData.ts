import { useCallback, useEffect, useState } from "react";
import {
  clearGitHistoryCache,
  fetchGitHistory,
  fetchGitStatus,
  getCachedGitHistory,
  getCachedGitHistoryHead,
  type GitHistoryPayload,
  type GitStatusPayload,
} from "../services/git";
import { GIT_HISTORY_EXPANDED_STORAGE_KEY, GIT_STATUS_EXPANDED_STORAGE_KEY, loadBooleanRecord, loadStringBooleanRecord } from "./appSupport";

/**
 * 当前项目的 git 数据：状态 / 历史（含分页）/ 各 root 的缓存与折叠态。
 *
 * 每个 root 都有一份 (见 *ByRoot)，当前 root 额外有一份「无前缀」的镜像 —— 视图只读镜像，
 * 跨项目面板读 ByRoot。切 root 时靠 shouldApply() 判据防止迟到响应写进错的镜像。
 */
export function useGitData({
  currentRootId,
  currentRootIdRef,
  scopedRootKey,
  getNodeIdForRoot,
  isGitRepo,
}: {
  currentRootId: string | null;
  currentRootIdRef: React.MutableRefObject<string | null>;
  scopedRootKey: (rootId: string) => string;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  isGitRepo: (rootId: string) => boolean;
}) {
  const [gitStatus, setGitStatus] = useState<GitStatusPayload | null>(null);
  const [gitStatusLoading, setGitStatusLoading] = useState(false);
  const [gitHistory, setGitHistory] = useState<GitHistoryPayload | null>(null);
  const [gitHistoryLoading, setGitHistoryLoading] = useState(false);
  const [gitHistoryLoadingMore, setGitHistoryLoadingMore] = useState(false);
  const [gitStatusByRoot, setGitStatusByRoot] = useState<Record<string, GitStatusPayload | null>>({});
  const [gitStatusLoadingByRoot, setGitStatusLoadingByRoot] = useState<Record<string, boolean>>({});
  const [gitHistoryByRoot, setGitHistoryByRoot] = useState<Record<string, GitHistoryPayload | null>>({});
  const [gitHistoryLoadingByRoot, setGitHistoryLoadingByRoot] = useState<Record<string, boolean>>({});
  const [gitStatusExpandedByRoot, setGitStatusExpandedByRoot] = useState<Record<string, boolean>>(() =>
    loadBooleanRecord(GIT_STATUS_EXPANDED_STORAGE_KEY),
  );
  const [gitHistoryExpandedByRoot, setGitHistoryExpandedByRoot] = useState<Record<string, Record<string, boolean>>>(() =>
    loadStringBooleanRecord(GIT_HISTORY_EXPANDED_STORAGE_KEY),
  );

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    window.localStorage.setItem(GIT_STATUS_EXPANDED_STORAGE_KEY, JSON.stringify(gitStatusExpandedByRoot));
  }, [gitStatusExpandedByRoot]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    window.localStorage.setItem(GIT_HISTORY_EXPANDED_STORAGE_KEY, JSON.stringify(gitHistoryExpandedByRoot));
  }, [gitHistoryExpandedByRoot]);

  const refreshGitStatus = useCallback(async (rootID: string) => {
    if (!rootID) {
      if (!currentRootIdRef.current) {
        setGitStatus(null);
        setGitStatusLoading(false);
      }
      return null;
    }
    const shouldApply = () => currentRootIdRef.current === rootID;
    if (!isGitRepo(rootID)) {
      const fallback = {
        available: false,
        dirty_count: 0,
        items: [],
      } as GitStatusPayload;
      setGitStatusByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: fallback }));
      setGitStatusLoadingByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: false }));
      if (shouldApply()) {
        setGitStatus(fallback);
        setGitStatusLoading(false);
      }
      return fallback;
    }
    setGitStatusLoadingByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: true }));
    if (shouldApply()) {
      setGitStatusLoading(true);
    }
    try {
      const next = await fetchGitStatus(rootID, getNodeIdForRoot(rootID));
      setGitStatusByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: next }));
      if (shouldApply()) {
        setGitStatus(next);
      }
      return next;
    } catch (err) {
      console.error("[git.status] failed", { rootID, err });
      const fallback = {
        available: false,
        dirty_count: 0,
        items: [],
      } as GitStatusPayload;
      setGitStatusByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: fallback }));
      if (shouldApply()) {
        setGitStatus(fallback);
      }
      return fallback;
    } finally {
      setGitStatusLoadingByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: false }));
      if (shouldApply()) {
        setGitStatusLoading(false);
      }
    }
  }, [currentRootIdRef, getNodeIdForRoot, isGitRepo, scopedRootKey]);

  const refreshGitHistory = useCallback(async (
    rootID: string,
    options?: { force?: boolean; waitForIncremental?: boolean },
  ) => {
    if (!rootID) {
      setGitHistory(null);
      setGitHistoryLoading(false);
      return null;
    }
    const shouldApply = () => currentRootIdRef.current === rootID;
    if (!options?.force) {
      const cachedHead = getCachedGitHistoryHead(rootID, 10, getNodeIdForRoot(rootID));
      if (cachedHead && cachedHead.items.length > 0) {
        setGitHistoryByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: cachedHead }));
        if (shouldApply()) {
          setGitHistory(cachedHead);
        }
        const newest = cachedHead.items[0]?.hash || "";
        if (newest) {
          const refreshAfterNewest = async () => {
            try {
              const next = await fetchGitHistory(rootID, { afterCommit: newest, nodeId: getNodeIdForRoot(rootID) });
              if (next.commit_missing || (next.items || []).length > 0) {
                clearGitHistoryCache(rootID, getNodeIdForRoot(rootID));
                const fresh = await fetchGitHistory(rootID, { force: true, nodeId: getNodeIdForRoot(rootID) });
                setGitHistoryByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: fresh }));
                if (shouldApply()) {
                  setGitHistory(fresh);
                }
                return fresh;
              }
              const fresh = getCachedGitHistoryHead(rootID, 10, getNodeIdForRoot(rootID)) || next;
              setGitHistoryByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: fresh }));
              if (shouldApply()) {
                setGitHistory(fresh);
              }
              return fresh;
            } catch (err) {
              console.error("[git.history.after] failed", { rootID, afterCommit: newest, err });
              return cachedHead;
            }
          };
          if (options?.waitForIncremental) {
            return refreshAfterNewest();
          }
          void refreshAfterNewest();
        }
        return cachedHead;
      }
    }
    setGitHistoryLoadingByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: true }));
    if (shouldApply()) {
      setGitHistoryLoading(true);
    }
    try {
      const next = await fetchGitHistory(rootID, { force: options?.force, nodeId: getNodeIdForRoot(rootID) });
      if (next.commit_missing) {
        clearGitHistoryCache(rootID, getNodeIdForRoot(rootID));
        const fresh = await fetchGitHistory(rootID, { force: true, nodeId: getNodeIdForRoot(rootID) });
        setGitHistoryByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: fresh }));
        if (shouldApply()) {
          setGitHistory(fresh);
        }
        return fresh;
      }
      setGitHistoryByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: next }));
      if (shouldApply()) {
        setGitHistory(next);
      }
      return next;
    } catch (err) {
      console.error("[git.history] failed", { rootID, err });
      const fallback = { available: false, items: [], has_more: false } as GitHistoryPayload;
      setGitHistoryByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: fallback }));
      if (shouldApply()) {
        setGitHistory(fallback);
      }
      return fallback;
    } finally {
      setGitHistoryLoadingByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: false }));
      if (shouldApply()) {
        setGitHistoryLoading(false);
      }
    }
  }, [currentRootIdRef, getNodeIdForRoot, scopedRootKey]);

  const loadMoreGitHistory = useCallback(async (targetRootID?: string) => {
    const rootID = targetRootID || currentRootIdRef.current;
    if (!rootID || gitHistoryLoadingMore) {
      return;
    }
    const currentItems =
      (targetRootID ? gitHistoryByRoot[scopedRootKey(rootID)]?.items : gitHistory?.items) || [];
    const beforeCommit = currentItems[currentItems.length - 1]?.hash || "";
    if (!beforeCommit) {
      return;
    }
    setGitHistoryLoadingMore(true);
    try {
      const next = await fetchGitHistory(rootID, { beforeCommit, nodeId: getNodeIdForRoot(rootID) });
      if (next.commit_missing) {
        clearGitHistoryCache(rootID, getNodeIdForRoot(rootID));
        const fresh = await fetchGitHistory(rootID, { force: true, nodeId: getNodeIdForRoot(rootID) });
        setGitHistoryByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: fresh }));
        if (currentRootIdRef.current === rootID) {
          setGitHistory(fresh);
        }
        return;
      }
      const cached = getCachedGitHistory(rootID, getNodeIdForRoot(rootID));
      const loadedCount = currentItems.length + next.items.length;
      if (currentRootIdRef.current === rootID) {
        if (cached) {
          setGitHistory({
            ...cached,
            items: cached.items.slice(0, loadedCount),
            has_more: cached.items.length > loadedCount || cached.has_more,
          });
        } else {
          setGitHistory(next);
        }
      }
      setGitHistoryByRoot((prev) => ({
        ...prev,
        [scopedRootKey(rootID)]: cached
          ? {
              ...cached,
              items: cached.items.slice(0, loadedCount),
              has_more: cached.items.length > loadedCount || cached.has_more,
            }
          : next,
      }));
    } catch (err) {
      console.error("[git.history.more] failed", { rootID, beforeCommit, err });
    } finally {
      setGitHistoryLoadingMore(false);
    }
  }, [currentRootIdRef, getNodeIdForRoot, gitHistory, gitHistoryByRoot, gitHistoryLoadingMore, scopedRootKey]);

  return {
    gitStatus,
    setGitStatus,
    gitStatusLoading,
    setGitStatusLoading,
    gitHistory,
    setGitHistory,
    gitHistoryLoading,
    setGitHistoryLoading,
    gitHistoryLoadingMore,
    gitStatusByRoot,
    setGitStatusByRoot,
    gitStatusLoadingByRoot,
    gitHistoryByRoot,
    setGitHistoryByRoot,
    gitHistoryLoadingByRoot,
    gitStatusExpandedByRoot,
    setGitStatusExpandedByRoot,
    gitHistoryExpandedByRoot,
    setGitHistoryExpandedByRoot,
    refreshGitStatus,
    refreshGitHistory,
    loadMoreGitHistory,
  };
}
