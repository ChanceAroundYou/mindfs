import { useCallback, useEffect, useState } from "react";
import { fetchGitStatusByPath, fetchGitWorktrees, type GitStatusPayload, type GitWorktreeItem } from "../services/git";
import { type ProjectTreeTab } from "../components/FileTree";
import { useI18n } from "../i18n";

/**
 * 左树「Worktrees」标签页的数据：列表按 root 缓存、展开项的状态按路径缓存。
 * 与 ActionBar 的新会话 worktree 选项是两回事（那个只管「新建时用哪个分支」）。
 */
export function useProjectTreeWorktrees({
  currentRootId,
  currentRootIdRef,
  projectTreeTab,
  scopedRootKey,
  getNodeIdForRoot,
  knownTaskWorktreePathsRef,
}: {
  currentRootId: string | null;
  currentRootIdRef: React.MutableRefObject<string | null>;
  projectTreeTab: ProjectTreeTab;
  scopedRootKey: (rootId: string) => string;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  knownTaskWorktreePathsRef: React.MutableRefObject<Set<string>>;
}) {
  const { t } = useI18n();
  const [worktreeItemsByRoot, setWorktreeItemsByRoot] = useState<Record<string, GitWorktreeItem[]>>({});
  const [worktreeLoadingByRoot, setWorktreeLoadingByRoot] = useState<Record<string, boolean>>({});
  const [worktreeErrorByRoot, setWorktreeErrorByRoot] = useState<Record<string, string>>({});
  const [expandedWorktreeByRoot, setExpandedWorktreeByRoot] = useState<Record<string, string>>({});
  const [worktreeStatusByPath, setWorktreeStatusByPath] = useState<Record<string, GitStatusPayload | null>>({});
  const [worktreeStatusLoadingByPath, setWorktreeStatusLoadingByPath] = useState<Record<string, boolean>>({});

  const loadProjectTreeWorktrees = useCallback(async (rootID: string): Promise<GitWorktreeItem[]> => {
    if (!rootID) {
      return [];
    }
    setWorktreeLoadingByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: true }));
    setWorktreeErrorByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: "" }));
    try {
      const payload = await fetchGitWorktrees(rootID, getNodeIdForRoot(rootID));
      (payload.items || []).forEach((item) => {
        if (item.path) {
          knownTaskWorktreePathsRef.current.add(item.path);
        }
      });
      const items = (payload.items || []).filter((item) => !!item.branch);
      setWorktreeItemsByRoot((prev) => ({
        ...prev,
        [scopedRootKey(rootID)]: items,
      }));
      return items;
    } catch (error) {
      setWorktreeItemsByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: [] }));
      setWorktreeErrorByRoot((prev) => ({
        ...prev,
        [scopedRootKey(rootID)]: error instanceof Error ? error.message : t("worktree.loadFailed"),
      }));
      return [];
    } finally {
      setWorktreeLoadingByRoot((prev) => ({ ...prev, [scopedRootKey(rootID)]: false }));
    }
  }, [getNodeIdForRoot, knownTaskWorktreePathsRef, scopedRootKey, t]);

  const loadProjectTreeWorktreeStatus = useCallback(async (worktreePath: string, rootId?: string) => {
    if (!worktreePath) {
      return;
    }
    setWorktreeStatusLoadingByPath((prev) => ({ ...prev, [worktreePath]: true }));
    try {
      const status = await fetchGitStatusByPath(worktreePath, getNodeIdForRoot(String(rootId || "")) as string | undefined);
      setWorktreeStatusByPath((prev) => ({ ...prev, [worktreePath]: status }));
    } catch (error) {
      console.error("[git.worktree.status] failed", { worktreePath, error });
      setWorktreeStatusByPath((prev) => ({
        ...prev,
        [worktreePath]: { available: false, dirty_count: 0, items: [] } as GitStatusPayload,
      }));
    } finally {
      setWorktreeStatusLoadingByPath((prev) => ({ ...prev, [worktreePath]: false }));
    }
  }, [getNodeIdForRoot]);

  // 切到 Worktrees 标签页时按需拉一次（已有数据或正在拉就不重复）
  useEffect(() => {
    if (projectTreeTab !== "worktrees" || !currentRootId) {
      return;
    }
    if (worktreeItemsByRoot[scopedRootKey(currentRootId)] || worktreeLoadingByRoot[scopedRootKey(currentRootId)]) {
      return;
    }
    void loadProjectTreeWorktrees(currentRootId);
  }, [currentRootId, loadProjectTreeWorktrees, projectTreeTab, worktreeItemsByRoot, worktreeLoadingByRoot, scopedRootKey]);

  /** 点了某个 worktree：展开则拉状态，再点收起。 */
  const toggleProjectTreeWorktree = useCallback(async (root: string, worktreePath: string) => {
    if (expandedWorktreeByRoot[scopedRootKey(root)] === worktreePath) {
      setExpandedWorktreeByRoot((prev) => ({ ...prev, [scopedRootKey(root)]: "" }));
      return;
    }
    setExpandedWorktreeByRoot({ [scopedRootKey(root)]: worktreePath });
    await loadProjectTreeWorktreeStatus(worktreePath, root);
  }, [expandedWorktreeByRoot, loadProjectTreeWorktreeStatus, scopedRootKey]);

  /** 刷新按钮：重拉列表 + 只刷当前展开项的状态。 */
  const refreshProjectTreeWorktrees = useCallback(async (root: string) => {
    const items = await loadProjectTreeWorktrees(root);
    const expandedPath = expandedWorktreeByRoot[scopedRootKey(root)] || "";
    if (expandedPath && items.some((item) => item.path === expandedPath)) {
      await loadProjectTreeWorktreeStatus(expandedPath, root);
    }
  }, [expandedWorktreeByRoot, loadProjectTreeWorktreeStatus, loadProjectTreeWorktrees, scopedRootKey]);

  /** 相关会话指定了 worktree：直接展开它（同一时间只展开一个）。 */
  const expandProjectTreeWorktree = useCallback(async (rootID: string, worktreePath: string) => {
    if (!rootID || !worktreePath) {
      return;
    }
    // 同一时间只展开一个工作树：整对象写入会清掉其它已展开项
    setExpandedWorktreeByRoot({ [scopedRootKey(rootID)]: worktreePath });
    await loadProjectTreeWorktreeStatus(worktreePath, rootID);
  }, [loadProjectTreeWorktreeStatus, scopedRootKey]);

  /**
   * 任务卡片上的 worktree：展开它并（必要时）重拉列表和状态。
   * force=false 时同一路径只处理一次——WS 广播会把同一条重复推很多遍。
   */
  const refreshTaskWorktree = useCallback(async (rootId: string, worktreePath: string, force = true) => {
    const normalizedRootId = String(rootId || "").trim();
    const normalizedWorktreePath = String(worktreePath || "").trim();
    if (!normalizedRootId || !normalizedWorktreePath) {
      return;
    }
    if (!force && knownTaskWorktreePathsRef.current.has(normalizedWorktreePath)) {
      return;
    }
    knownTaskWorktreePathsRef.current.add(normalizedWorktreePath);
    // 同一时间只展开一个工作树：整对象写入会清掉其它已展开项
    setExpandedWorktreeByRoot({ [scopedRootKey(normalizedRootId)]: normalizedWorktreePath });
    setWorktreeLoadingByRoot((prev) => ({ ...prev, [scopedRootKey(normalizedRootId)]: true }));
    setWorktreeErrorByRoot((prev) => ({ ...prev, [scopedRootKey(normalizedRootId)]: "" }));
    try {
      const payload = await fetchGitWorktrees(normalizedRootId, getNodeIdForRoot(normalizedRootId));
      (payload.items || []).forEach((item) => {
        if (item.path) {
          knownTaskWorktreePathsRef.current.add(item.path);
        }
      });
      setWorktreeItemsByRoot((prev) => ({
        ...prev,
        [scopedRootKey(normalizedRootId)]: (payload.items || []).filter((item) => !!item.branch),
      }));
    } catch (error) {
      knownTaskWorktreePathsRef.current.delete(normalizedWorktreePath);
      setWorktreeErrorByRoot((prev) => ({
        ...prev,
        [scopedRootKey(normalizedRootId)]: error instanceof Error ? error.message : t("worktree.loadFailed"),
      }));
    } finally {
      setWorktreeLoadingByRoot((prev) => ({ ...prev, [scopedRootKey(normalizedRootId)]: false }));
    }
    setWorktreeStatusLoadingByPath((prev) => ({ ...prev, [normalizedWorktreePath]: true }));
    try {
      const status = await fetchGitStatusByPath(normalizedWorktreePath, getNodeIdForRoot(normalizedRootId));
      setWorktreeStatusByPath((prev) => ({ ...prev, [normalizedWorktreePath]: status }));
    } catch {
      setWorktreeStatusByPath((prev) => ({ ...prev, [normalizedWorktreePath]: null }));
    } finally {
      setWorktreeStatusLoadingByPath((prev) => ({ ...prev, [normalizedWorktreePath]: false }));
    }
  }, [getNodeIdForRoot, knownTaskWorktreePathsRef, scopedRootKey, t]);

  return {
    worktreeItemsByRoot,
    worktreeLoadingByRoot,
    worktreeErrorByRoot,
    expandedWorktreeByRoot,
    worktreeStatusByPath,
    worktreeStatusLoadingByPath,
    loadProjectTreeWorktrees,
    loadProjectTreeWorktreeStatus,
    toggleProjectTreeWorktree,
    expandProjectTreeWorktree,
    refreshProjectTreeWorktrees,
    refreshTaskWorktree,
  };
}
