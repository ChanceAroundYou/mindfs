import { useCallback, useEffect } from "react";
import type { ProjectTreeTab } from "../components/file/fileTreeTypes";
import type { FileEntry } from "../services/prefs/directorySort";
import type { RelatedFile, RelatedWorktree } from "../services/session";
import { sessionService, setCachedSessionRelatedFiles } from "../services/session";
import { syncNodesFromServer } from "../services/net/nodeRegistry";
import type { KanbanTask } from "../services/tasks";
import { scopeKey } from "../shared/scope";

type ProjectTreeActionsProps = {
  currentRootId: string | null;
  currentRootIdRef: { current: string | null };
  relatedSessionRootId: string | null | undefined;
  relatedSessionKey: string | undefined;
  selectedKanbanTask: KanbanTask | null | undefined;
  taskSessionKeysByIdRef: { current: Record<string, string[]> };
  selectedDirRef: { current: string | null };
  entriesByPathRef: { current: Record<string, FileEntry[]> };
  fileRef: { current: { path?: string } | null };
  relatedWorktree: RelatedWorktree | null;
  projectTreeTab: ProjectTreeTab;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  refreshTreeDir: (root: string, dir: string, force: boolean) => Promise<unknown>;
  refreshCurrentFileContent: (root: string, path: string) => unknown;
  refreshGitStatus: (root: string) => unknown;
  refreshGitHistory: (root: string, opts: { waitForIncremental: boolean }) => unknown;
  refreshProjectTreeWorktrees: (root: string) => unknown;
  expandProjectTreeWorktree: (rootId: string, worktreePath: string) => unknown;
  refreshTaskRelatedFiles: (root: string, taskId: string, sessionKeys: string[]) => Promise<unknown>;
  updateSessionRelatedFilesForKey: (root: string, sessionKey: string, files: RelatedFile[]) => unknown;
  refreshManagedRoots: () => Promise<unknown>;
};

/**
 * 项目树面板的刷新动作：文件 / git / worktree / 关联文件四个 tab 的强制重拉，
 * 加上「管理节点」面板的注册表同步，以及切到 worktree tab 时自动展开当前工作树。
 *
 * 刻意保留每个 case 的 `await` 与并发结构（Promise.all 的成员与顺序不变），
 * 也不合并 refreshProjectTreeRelatedFiles 的两条分支 —— 任务侧与会话侧的
 * 数据来源不同，合并会改变请求条数。
 */
export function useProjectTreeActions({
  currentRootId,
  currentRootIdRef,
  relatedSessionRootId,
  relatedSessionKey,
  selectedKanbanTask,
  taskSessionKeysByIdRef,
  selectedDirRef,
  entriesByPathRef,
  fileRef,
  relatedWorktree,
  projectTreeTab,
  getNodeIdForRoot,
  refreshTreeDir,
  refreshCurrentFileContent,
  refreshGitStatus,
  refreshGitHistory,
  refreshProjectTreeWorktrees,
  expandProjectTreeWorktree,
  refreshTaskRelatedFiles,
  updateSessionRelatedFilesForKey,
  refreshManagedRoots,
}: ProjectTreeActionsProps) {
  const refreshProjectTreeRelatedFiles = useCallback(async () => {
    try {
      if (selectedKanbanTask) {
        const root = selectedKanbanTask.root_id || relatedSessionRootId || currentRootId || "";
        const taskId = String(selectedKanbanTask.id || "");
        const sessionKeys = Array.from(new Set(
          [
            ...(taskSessionKeysByIdRef.current[taskId] || []),
            selectedKanbanTask.main_session_key,
          ]
            .map((key) => String(key || "").trim())
            .filter(Boolean),
        ));
        if (root && taskId && sessionKeys.length > 0) {
          await refreshTaskRelatedFiles(root, taskId, sessionKeys);
        }
        return;
      }
      const root = relatedSessionRootId || currentRootId || "";
      const sessionKey = String(relatedSessionKey || "").trim();
      if (!root || !sessionKey) {
        return;
      }
      const relatedFiles = await sessionService.getSessionRelatedFiles(root, sessionKey);
      await setCachedSessionRelatedFiles(root, sessionKey, relatedFiles);
      updateSessionRelatedFilesForKey(root, sessionKey, relatedFiles);
    } catch (error) {
      console.error("[session.related_files] manual refresh failed", { error });
    }
  }, [
    currentRootId,
    refreshTaskRelatedFiles,
    relatedSessionKey,
    relatedSessionRootId,
    selectedKanbanTask,
    taskSessionKeysByIdRef,
    updateSessionRelatedFilesForKey,
  ]);

  // 「管理节点」面板的刷新：先拉节点注册表（拿别的设备改过的节点），
  // 再让 refreshManagedRoots 逐节点重拉 dirs 并触发多项目会话/回复态重载。
  // 不要再额外调 loadManagedRootPayloads({force:true})——refreshManagedRoots 内部已经会调它。
  const handleNodeManagerRefresh = useCallback(async () => {
    await syncNodesFromServer().catch(() => {});
    await refreshManagedRoots();
  }, [refreshManagedRoots]);

  const handleProjectTreeRefresh = useCallback(async (tab: ProjectTreeTab) => {
    const root = currentRootIdRef.current;
    if (!root) {
      return;
    }
    switch (tab) {
      case "files": {
        // 强制拉取真实状态：当前目录 + 侧边栏已加载（展开/访问过）的所有目录全部重拉，
        // 而非仅刷新选中目录——展开的子目录若不重拉，刷新后仍显示旧条目。
        const selDir = selectedDirRef.current === root ? "." : (selectedDirRef.current || ".");
        const treeBase = scopeKey(getNodeIdForRoot(root) ?? "", root);
        const targets = new Set<string>([selDir]);
        for (const key of Object.keys(entriesByPathRef.current)) {
          if (key === treeBase) {
            targets.add(".");
          } else if (key.startsWith(`${treeBase}:`)) {
            targets.add(key.slice(treeBase.length + 1));
          }
        }
        await Promise.all(
          [...targets].map((dir) => refreshTreeDir(root, dir, dir === selDir)),
        );
        if (fileRef.current?.path) {
          // 主视图正打开文件时，刷新同时强制重读该文件内容（WS 断开时唯一的重读入口）
          void refreshCurrentFileContent(root, fileRef.current.path);
        }
        return;
      }
      case "git":
        await Promise.all([
          refreshGitStatus(root),
          refreshGitHistory(root, { waitForIncremental: true }),
        ]);
        return;
      case "worktrees":
        await refreshProjectTreeWorktrees(root);
        return;
      case "related":
        await Promise.all([
          refreshProjectTreeRelatedFiles(),
          refreshGitStatus(root),
        ]);
    }
  }, [
    currentRootIdRef,
    entriesByPathRef,
    fileRef,
    getNodeIdForRoot,
    refreshCurrentFileContent,
    refreshGitHistory,
    refreshGitStatus,
    refreshProjectTreeRelatedFiles,
    refreshProjectTreeWorktrees,
    refreshTreeDir,
    selectedDirRef,
  ]);

  useEffect(() => {
    const rootID = String(relatedWorktree?.root_id || "");
    const worktreePath = String(relatedWorktree?.path || "");
    if (projectTreeTab !== "worktrees" || !rootID || !worktreePath) {
      return;
    }
    void expandProjectTreeWorktree(rootID, worktreePath);
  }, [
    expandProjectTreeWorktree,
    projectTreeTab,
    relatedWorktree?.path,
    relatedWorktree?.root_id,
  ]);

  return {
    refreshProjectTreeRelatedFiles,
    handleNodeManagerRefresh,
    handleProjectTreeRefresh,
  };
}
