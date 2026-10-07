/**
 * 关联文件 diff 打开 —— A6c 簇（1 个单元，~86 行）。
 *
 * 覆盖：从会话关联文件列表打开对应文件的 git diff（含 worktree / 主 checkout
 * 两种路径与只读文件回退）。
 *
 * 做法：hook，参数面 UseRelatedFileDiffParams 即完整契约；函数体逐字搬入
 * （保持源码正则断言可命中，配合 source-map 注册）。
 */
import { useCallback, useMemo, type MutableRefObject, type Dispatch, type SetStateAction } from "react";
import { ManagedRootPayload, useResponsive } from "./appMisc";
import { MainViewMode, URLState, joinDisplayPath, relativeDisplayPathFromRoot } from "./appPath";
import { RelatedFileClickTarget, SessionItem, relatedFileSelectionKey } from "./appSession";
import { useActionHandlers } from "./useActionHandlers";
import { useCompletionSound } from "./useCompletionSound";
import { useExternalSessionImport } from "./useExternalSessionImport";
import { useFileRefresh } from "./useFileRefresh";
import { useGitActions } from "./useGitActions";
import { useGitData } from "./useGitData";
import { useManagedRoots } from "./useManagedRoots";
import { useMultiProjectSessions } from "./useMultiProjectSessions";
import { usePluginHandlers } from "./usePluginHandlers";
import { useProjectLifecycle } from "./useProjectLifecycle";
import { useProjectTreeWorktrees } from "./useProjectTreeWorktrees";
import { useSendMessage } from "./useSendMessage";
import { useSessionArchive } from "./useSessionArchive";
import { useSessionCache } from "./useSessionCache";
import { useSessionLifecycle } from "./useSessionLifecycle";
import { useSessionSearch } from "./useSessionSearch";
import { useSessionSidebarView } from "./useSessionSidebarView";
import { useSessionStreamCache } from "./useSessionStreamCache";
import { useTaskTemplates } from "./useTaskTemplates";
import { useI18n } from "../i18n";
import { type FilePayload } from "../services/file";
import { type GitDiffPayload, fetchGitRelatedFileDiff } from "../services/git";
import { ProtectedAPIError } from "../services/net/api";
import { reportError } from "../services/net/error";

export type UseRelatedFileDiffParams = {
  actionHandlers: ReturnType<typeof useActionHandlers>["handlers"];
  currentRootIdRef: MutableRefObject<string | null>;
  file: FilePayload | null;
  fileOpenRequestRef: MutableRefObject<number>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  gitStatus: ReturnType<typeof useGitData>["gitStatus"];
  managedRootByIdRef: MutableRefObject<Record<string, ManagedRootPayload>>;
  openGitDiff: ReturnType<typeof useGitActions>["openGitDiff"];
  pluginQuery: Record<string, string>;
  replaceURLState: (next: URLState) => any;
  resetLocksForRootTransition: (targetRoot: string | null | undefined) => any;
  setCurrentRootId: Dispatch<SetStateAction<string | null>>;
  setFile: Dispatch<SetStateAction<FilePayload | null>>;
  setGitDiff: Dispatch<SetStateAction<GitDiffPayload | null>>;
  setIsLeftOpen: Dispatch<SetStateAction<boolean>>;
  setRelatedSelectedFileKey: Dispatch<SetStateAction<string>>;
  setSelectedSession: Dispatch<SetStateAction<SessionItem | null>>;
  setSelectedSessionLoading: Dispatch<SetStateAction<boolean>>;
  switchMainView: (mode: MainViewMode) => any;
};

export function useRelatedFileDiff(params: UseRelatedFileDiffParams) {
  const { t } = useI18n();
  const { isMobile } = useResponsive();
  const {
    actionHandlers,
    currentRootIdRef,
    file,
    fileOpenRequestRef,
    getNodeIdForRoot,
    gitStatus,
    managedRootByIdRef,
    openGitDiff,
    pluginQuery,
    replaceURLState,
    resetLocksForRootTransition,
    setCurrentRootId,
    setFile,
    setGitDiff,
    setIsLeftOpen,
    setRelatedSelectedFileKey,
    setSelectedSession,
    setSelectedSessionLoading,
    switchMainView,
  } = params;

  const openRelatedFileDiff = useCallback(
    async (rootID: string, file: RelatedFileClickTarget) => {
      const path = String(file?.path || "").trim();
      const head = String(file?.head || "").trim();
      const repoKind = String(file?.repo_kind || "").trim();
      if (!rootID || !path) {
        return;
      }
      setRelatedSelectedFileKey(relatedFileSelectionKey(file));
      if (repoKind === "plain") {
        actionHandlers.open({ path, root: rootID, preserveRelatedSelection: true });
        return;
      }
      if (!head && !file?.repo_path) {
        const gitItem = (gitStatus?.items || []).find(
          (item) => item.path === path,
        );
        if (gitItem) {
          void openGitDiff(rootID, gitItem, { preserveRelatedSelection: true });
          return;
        }
        actionHandlers.open({ path, root: rootID, preserveRelatedSelection: true });
        return;
      }
      fileOpenRequestRef.current += 1;
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
        const next = await fetchGitRelatedFileDiff(rootID, file, getNodeIdForRoot(rootID));
        const rootPath = managedRootByIdRef.current[rootID]?.root_path;
        const repoPath = String(file?.repo_path || "").trim();
        const displayPath = repoPath
          ? relativeDisplayPathFromRoot(rootPath, joinDisplayPath(repoPath, path))
          : path;
        if (displayPath) {
          next.display_path = displayPath;
        }
        setGitDiff(next);
        resetLocksForRootTransition(rootID);
        if (currentRootIdRef.current !== rootID) {
          setCurrentRootId(rootID);
        }
        if (isMobile) {
          setIsLeftOpen(false);
        }
      } catch (err) {
        const message =
      err instanceof Error ? err.message : t("git.relatedFileDiffFailed");
        console.error("[git.related-file.diff] failed", {
          rootID,
          path,
          head,
          err,
        });
        reportError("git.related_file_diff_failed", message, {
          severity: "warning",
          recoverable: true,
          details: {
            root: rootID,
            path,
            head,
            payload: err instanceof ProtectedAPIError ? err.payload : undefined,
          },
        });
      }
    },
    [
      actionHandlers,
      gitStatus,
      isMobile,
      openGitDiff,
      replaceURLState,
      switchMainView,
      resetLocksForRootTransition,
    ],
  );

  return {
    openRelatedFileDiff,
  };
}
