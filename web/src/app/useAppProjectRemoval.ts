import { useCallback } from "react";
import { appURL } from "../services/net/base";
import { protectedJSON as apiProtectedJSON } from "../services/net/api";
import { confirmDialog } from "../services/dialog";
import { reportError } from "../services/net/error";
import { removeGitWorktree } from "../services/git";
import type { ManagedRootPayload } from "./appMisc";
import { normalizeComparableRootPath } from "./appUtils";
import type { MessageKey, MessageParams } from "../i18n/index";

/**
 * 移除当前项目 / 当前 worktree，以及按路径找 managed root。
 * 从 App.tsx 抽出（2026-10-08）：三者共用 managedRootByIdRef 与
 * clearRootScopedClientState 的清场语义——删项目和删 worktree 走的是同一条
 * 「清根作用域 → 刷新根列表」链路，散在 App 里容易被后来者只改一半。
 */
export type UseAppProjectRemovalParams = {
  clearRootScopedClientState: (rootID: string, options?: { removeLastRoot?: boolean }) => void;
  currentRootIdRef: React.RefObject<string | null>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  managedRootByIdRef: React.RefObject<Record<string, ManagedRootPayload>>;
  refreshManagedRoots: () => Promise<void>;
  t: (key: MessageKey, params?: MessageParams) => string;
};

/**
 * 注意 getNodeIdForRoot 是被 App 抽出去的：删项目要打数据面（跟选中节点走），
 * 而 worktree 永远在本机，所以两个删除函数里它出现与否不一样，不是漏写。
 */
export function useAppProjectRemoval(params: UseAppProjectRemovalParams) {
  const { clearRootScopedClientState, currentRootIdRef, getNodeIdForRoot, managedRootByIdRef, refreshManagedRoots, t } = params;

  const findManagedRootByPath = useCallback((path: string): ManagedRootPayload | null => {
    const target = normalizeComparableRootPath(path);
    if (!target) {
      return null;
    }
    for (const root of Object.values(managedRootByIdRef.current)) {
      if (normalizeComparableRootPath(root.root_path || "") === target) {
        return root;
      }
    }
    return null;
  }, [normalizeComparableRootPath]);

  const handleRemoveCurrentRoot = useCallback(async () => {
    const rootID = currentRootIdRef.current;
    if (!rootID) {
      return;
    }
    const rootInfo = managedRootByIdRef.current[rootID];
    const rootPath = rootInfo?.root_path || "";
    if (!rootPath) {
      reportError("root.delete_failed", t("root.missingPathRemove"));
      return;
    }
    if (!await confirmDialog({ message: t("root.confirmRemove", { name: rootID }), danger: true })) {
      return;
    }
    try {
      await apiProtectedJSON<any>(
        appURL("/api/dirs", new URLSearchParams({ path: rootPath }), getNodeIdForRoot(rootID)),
        {
          method: "DELETE",
        },
      );
      clearRootScopedClientState(rootID, { removeLastRoot: true });
      await refreshManagedRoots();
    } catch (err) {
      reportError(
        "root.delete_failed",
        String((err as Error)?.message || t("root.removeFailed")),
      );
    }
  }, [clearRootScopedClientState, refreshManagedRoots, t]);

  const handleRemoveCurrentWorktree = useCallback(async () => {
    const rootID = currentRootIdRef.current;
    if (!rootID) {
      return;
    }
    if (!await confirmDialog({ message: t("worktree.confirmRemove", { name: rootID }), danger: true })) {
      return;
    }
    try {
      await removeGitWorktree(rootID);
      clearRootScopedClientState(rootID, { removeLastRoot: true });
      await refreshManagedRoots();
    } catch (err) {
      reportError(
        "git.worktree_remove_failed",
        String((err as Error)?.message || t("worktree.removeFailed")),
      );
    }
  }, [clearRootScopedClientState, refreshManagedRoots, t]);

  return {
    findManagedRootByPath,
    handleRemoveCurrentRoot,
    handleRemoveCurrentWorktree,
  };
}
