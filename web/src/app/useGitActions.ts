import { useCallback } from "react";
import {
  buildGitDiffCacheSignature,
  checkoutGitBranch,
  clearGitHistoryCache,
  commitGit,
  discardGitItem,
  fetchGitCommitDiff,
  fetchGitDiff,
  pullGit,
  pushGit,
  stageGitItem,
  unstageGitItem,
  type GitHistoryItem,
  type GitStatusItem,
  type GitStatusPayload,
} from "../services/git";
import { reportError } from "../services/error";
import { ProtectedAPIError } from "../services/api";
import { useI18n } from "../i18n";
import { type URLState } from "./appSupport";
import { alertDialog } from "../services/dialog";

/**
 * git 写操作 + diff 打开：checkout / pull / push / commit / stage / unstage / discard。
 *
 * 统一节奏：写操作返回新 status → 失效历史缓存 → 刷历史 → 若当前 root 就刷新视图与文件树。
 * 失败一律发可见提示（runGitAction 直接 alert，checkout 走 reportError）。
 */
export function useGitActions({
  currentRootIdRef,
  getNodeIdForRoot,
  scopedRootKey,
  selectedDirRef,
  fileOpenRequestRef,
  refreshGitHistory,
  refreshTreeDir,
  resetLocksForRootTransition,
  switchMainView,
  replaceURLState,
  isMobile,
  setGitStatus,
  setGitStatusByRoot,
  setGitDiff,
  setFile,
  setCurrentRootId,
  setIsLeftOpen,
  setRelatedSelectedFileKey,
  setSelectedSession,
  setSelectedSessionLoading,
}: {
  currentRootIdRef: React.MutableRefObject<string | null>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  scopedRootKey: (rootId: string) => string;
  selectedDirRef: React.MutableRefObject<string | null>;
  fileOpenRequestRef: React.MutableRefObject<number>;
  refreshGitHistory: (rootID: string, options?: { force?: boolean; waitForIncremental?: boolean }) => Promise<unknown>;
  refreshTreeDir: (rootID: string, dirPath: string, syncMain: boolean) => Promise<unknown>;
  resetLocksForRootTransition: (rootID: string) => void;
  switchMainView: (mode: any) => void;
  replaceURLState: (next: URLState) => void;
  isMobile: boolean;
  setGitStatus: (status: GitStatusPayload | null) => void;
  setGitStatusByRoot: React.Dispatch<React.SetStateAction<Record<string, GitStatusPayload | null>>>;
  setGitDiff: (diff: any) => void;
  setFile: (file: any) => void;
  setCurrentRootId: (rootId: string) => void;
  setIsLeftOpen: (open: boolean) => void;
  setRelatedSelectedFileKey: (key: string) => void;
  setSelectedSession: (session: any) => void;
  setSelectedSessionLoading: (loading: boolean) => void;
}) {
  const { t } = useI18n();

  const openGitDiff = useCallback(
    async (rootID: string, item: GitStatusItem, options?: { preserveRelatedSelection?: boolean; repoPath?: string }) => {
      if (!rootID || !item?.path) {
        return;
      }
      fileOpenRequestRef.current += 1;
      if (!options?.preserveRelatedSelection) {
        setRelatedSelectedFileKey("");
      }
      switchMainView("files");
      setSelectedSession(null);
      setSelectedSessionLoading(false);
      setFile(null);
      setGitDiff(null);
      replaceURLState({
        root: rootID,
        file: "",
        session: "",
        cursor: 0,
        pluginQuery: {},
      });
      try {
        const next = await fetchGitDiff(rootID, item.path, { nodeId: getNodeIdForRoot(rootID) as string | undefined,
          cacheSignature: buildGitDiffCacheSignature(item),
          repoPath: options?.repoPath,
        });
        setGitDiff(next);
        resetLocksForRootTransition(rootID);
        if (currentRootIdRef.current !== rootID) {
          setCurrentRootId(rootID);
        }
        if (isMobile) {
          setIsLeftOpen(false);
        }
      } catch (err) {
        console.error("[git.diff] failed", { rootID, path: item.path, err });
      }
    },
    [currentRootIdRef, fileOpenRequestRef, getNodeIdForRoot, isMobile, replaceURLState, resetLocksForRootTransition,
      setCurrentRootId, setFile, setGitDiff, setIsLeftOpen, setRelatedSelectedFileKey, setSelectedSession,
      setSelectedSessionLoading, switchMainView],
  );

  const openGitCommitDiff = useCallback(
    async (rootID: string, commit: GitHistoryItem, item: GitStatusItem) => {
      if (!rootID || !commit?.hash || !item?.path) {
        return;
      }
      fileOpenRequestRef.current += 1;
      setRelatedSelectedFileKey("");
      switchMainView("files");
      setSelectedSession(null);
      setSelectedSessionLoading(false);
      setFile(null);
      setGitDiff(null);
      replaceURLState({
        root: rootID,
        file: "",
        session: "",
        cursor: 0,
        pluginQuery: {},
      });
      try {
        const next = await fetchGitCommitDiff(rootID, commit.hash, item, getNodeIdForRoot(rootID));
        setGitDiff(next);
        resetLocksForRootTransition(rootID);
        if (currentRootIdRef.current !== rootID) {
          setCurrentRootId(rootID);
        }
        if (isMobile) {
          setIsLeftOpen(false);
        }
      } catch (err) {
        console.error("[git.commit.diff] failed", {
          rootID,
          commit: commit.hash,
          path: item.path,
          err,
        });
      }
    },
    [currentRootIdRef, fileOpenRequestRef, getNodeIdForRoot, isMobile, replaceURLState, resetLocksForRootTransition,
      setCurrentRootId, setFile, setGitDiff, setIsLeftOpen, setRelatedSelectedFileKey, setSelectedSession,
      setSelectedSessionLoading, switchMainView],
  );

  const switchGitBranch = useCallback(
    async (rootID: string, branch: string) => {
      if (!rootID || !branch) {
        return;
      }
      try {
        const nextStatus = await checkoutGitBranch(rootID, branch);
        clearGitHistoryCache(rootID, getNodeIdForRoot(rootID));
        setGitStatusByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: nextStatus }));
        await refreshGitHistory(rootID, { force: true });
        if (currentRootIdRef.current === rootID) {
          setGitStatus(nextStatus);
          setGitDiff(null);
          setFile(null);
          await refreshTreeDir(rootID, selectedDirRef.current || ".", true);
        }
      } catch (err) {
        const message = err instanceof Error ? err.message : t("git.checkoutFailed");
        console.error("[git.checkout] failed", {
          rootID,
          branch,
          message,
          payload: err instanceof ProtectedAPIError ? err.payload : undefined,
          err,
        });
        reportError("git.checkout_failed", message, {
          severity: "error",
          recoverable: true,
          details: {
            root: rootID,
            branch,
            payload: err instanceof ProtectedAPIError ? err.payload : undefined,
          },
        });
        throw err;
      }
    },
    [currentRootIdRef, getNodeIdForRoot, refreshGitHistory, refreshTreeDir, scopedRootKey, selectedDirRef,
      setFile, setGitDiff, setGitStatus, setGitStatusByRoot, t],
  );

  const applyGitActionResult = useCallback(
    async (rootID: string, nextStatus: GitStatusPayload, options?: { refreshHistory?: boolean; clearDiff?: boolean }) => {
      clearGitHistoryCache(rootID, getNodeIdForRoot(rootID));
      setGitStatusByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: nextStatus }));
      if (options?.refreshHistory !== false) {
        await refreshGitHistory(rootID, { force: true });
      }
      if (currentRootIdRef.current !== rootID) {
        return;
      }
      setGitStatus(nextStatus);
      if (options?.clearDiff) {
        setGitDiff(null);
      }
      await refreshTreeDir(rootID, selectedDirRef.current || ".", true);
    },
    [currentRootIdRef, getNodeIdForRoot, refreshGitHistory, refreshTreeDir, scopedRootKey, selectedDirRef,
      setGitDiff, setGitStatus, setGitStatusByRoot],
  );

  const runGitAction = useCallback(
    async (
      rootID: string,
      action: string,
      run: () => Promise<{ status: GitStatusPayload; output?: string }>,
      options?: { refreshHistory?: boolean; clearDiff?: boolean },
    ) => {
      if (!rootID) {
        return;
      }
      try {
        const result = await run();
        await applyGitActionResult(rootID, result.status, options);
      } catch (err) {
        const message = err instanceof Error ? err.message : t("common.actionFailed", { action });
        console.error(`[git.${action}] failed`, {
          rootID,
          message,
          payload: err instanceof ProtectedAPIError ? err.payload : undefined,
          err,
        });
        alertDialog(message);
        throw err;
      }
    },
    [applyGitActionResult, t],
  );

  const handleGitPull = useCallback(
    (rootID: string) =>
      runGitAction(rootID, "pull", () => pullGit(rootID), { clearDiff: true }),
    [runGitAction],
  );

  const handleGitPush = useCallback(
    (rootID: string) =>
      runGitAction(rootID, "push", () => pushGit(rootID)),
    [runGitAction],
  );

  const handleGitCommit = useCallback(
    (rootID: string, message: string) =>
      runGitAction(rootID, "commit", () => commitGit(rootID, message), { clearDiff: true }),
    [runGitAction],
  );

  const handleGitStageItem = useCallback(
    (rootID: string, item: GitStatusItem) =>
      runGitAction(rootID, "stage", () => stageGitItem(rootID, item), {
        refreshHistory: false,
        clearDiff: true,
      }),
    [runGitAction],
  );

  const handleGitUnstageItem = useCallback(
    (rootID: string, item: GitStatusItem) =>
      runGitAction(rootID, "unstage", () => unstageGitItem(rootID, item), {
        refreshHistory: false,
        clearDiff: true,
      }),
    [runGitAction],
  );

  const handleGitDiscardItem = useCallback(
    (rootID: string, item: GitStatusItem) =>
      runGitAction(rootID, "discard", () => discardGitItem(rootID, item), {
        refreshHistory: false,
        clearDiff: true,
      }),
    [runGitAction],
  );

  return {
    openGitDiff,
    openGitCommitDiff,
    switchGitBranch,
    applyGitActionResult,
    runGitAction,
    handleGitPull,
    handleGitPush,
    handleGitCommit,
    handleGitStageItem,
    handleGitUnstageItem,
    handleGitDiscardItem,
  };
}
