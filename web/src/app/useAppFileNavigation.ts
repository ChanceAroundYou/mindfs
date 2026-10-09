import { useCallback, useMemo } from "react";

import { reportError } from "../services/net/error";
import type { ActionHandlers } from "./useActionHandlers";
import type { GitFileStat } from "./appSession";
import type { GitDiffPayload, GitStatusPayload } from "../services/git";
import type { FilePayload } from "../services/file/types";
import type { MessageKey, MessageParams } from "../i18n";

export type UseAppFileNavigationParams = {
  file: FilePayload | null;
  currentRootIdRef: React.RefObject<string | null>;
  actionHandlers: ActionHandlers;
  setGitDiff: React.Dispatch<React.SetStateAction<GitDiffPayload | null>>;
  gitStatus: GitStatusPayload | null;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  notifiedNodeFailuresRef: React.RefObject<Set<string>>;
  refreshManagedRoots: () => Promise<void>;
  t: (key: MessageKey, params?: MessageParams) => string;
};

/**
 * 文件/目录路径被点开时往哪走，以及两处从文件树派生的只读视图。
 *
 * 四个 click handler 形状几乎一样（解析 root → 调 actionHandlers.open_dir 或
 * open），差别只在「点的是目录还是文件」和「要不要先清掉 git diff」。放一起
 * 是因为它们共享同一条路径解析规则：`"."` 代表项目根、`suppressTreeExpand`
 * 只在点根时开 —— 散开后很容易改一处漏三处。
 *
 * `notifyNodeLoadFailed` 是同一族的：节点加载失败也要走路径/根这套判断，
 * 且带「每个节点只报一次」的去重。
 */
export function useAppFileNavigation(params: UseAppFileNavigationParams) {
  const {
    file,
    currentRootIdRef,
    actionHandlers,
    setGitDiff,
    gitStatus,
    notifiedNodeFailuresRef,
    refreshManagedRoots,
    t,
  } = params;

  const notifyNodeLoadFailed = useCallback(
    (node: { id: string; name: string }) => {
      if (notifiedNodeFailuresRef.current.has(node.id)) return;
      notifiedNodeFailuresRef.current.add(node.id);
      reportError("node.load_failed", t("error.node.loadFailed", { name: node.name }), {
        severity: "warning",
        recoverable: true,
        details: { nodeId: node.id },
        retryAction: async () => {
          notifiedNodeFailuresRef.current.delete(node.id);
          await refreshManagedRoots();
        },
      });
    },
    [refreshManagedRoots, t],
  );

  const handleFileViewerPathClick = useCallback(
    (path: string) => {
      if (!file) return;
      const root = file.root || currentRootIdRef.current;
      if (!root) return;
      actionHandlers.open_dir({
        path: path === "." ? root : path,
        root,
        isRoot: path === ".",
        suppressTreeExpand: path === ".",
      });
    },
    [file, actionHandlers],
  );

  const handleFileViewerFileClick = useCallback(
    (path: string) => {
      if (!file) return;
      const root = file.root || currentRootIdRef.current;
      if (!root) return;
      actionHandlers.open({ path, root });
    },
    [file, actionHandlers],
  );

  const handleGitDiffPathClick = useCallback(
    (path: string) => {
      const root = currentRootIdRef.current;
      if (!root) return;
      setGitDiff(null);
      actionHandlers.open_dir({
        path: path === "." ? root : path,
        root,
        isRoot: path === ".",
        suppressTreeExpand: path === ".",
      });
    },
    [actionHandlers],
  );

  const handleDirectoryPathClick = useCallback(
    (path: string) => {
      const root = currentRootIdRef.current;
      if (!root) return;
      actionHandlers.open_dir({
        path: path === "." ? root : path,
        root,
        isRoot: path === ".",
        suppressTreeExpand: path === ".",
      });
    },
    [actionHandlers],
  );

  const gitFileStatsByPath = useMemo<Record<string, GitFileStat>>(() => {
    const items = gitStatus?.items || [];
    return Object.fromEntries(
      items.map((item) => [
        item.path,
        {
          status: item.status,
          additions: item.additions,
          deletions: item.deletions,
        },
      ]),
    );
  }, [gitStatus]);

  return {
    notifyNodeLoadFailed,
    handleFileViewerPathClick,
    handleFileViewerFileClick,
    handleGitDiffPathClick,
    handleDirectoryPathClick,
    gitFileStatsByPath,
  };
}