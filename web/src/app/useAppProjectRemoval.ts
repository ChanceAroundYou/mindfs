import { useCallback } from "react";
import { appURL } from "../services/net/base";
import { protectedJSON as apiProtectedJSON } from "../services/net/api";
import { confirmDialog } from "../services/dialog";
import { reportError } from "../services/net/error";
import { removeGitWorktree } from "../services/git";
import type { ManagedRootPayload } from "./appMisc";
import { rootNodeKey } from "./appPath";
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
  currentRootNodeIdRef: React.RefObject<string | null>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  managedRootByIdRef: React.RefObject<Record<string, ManagedRootPayload>>;
  managedRootByKeyRef: React.RefObject<Record<string, ManagedRootPayload>>;
  refreshManagedRoots: () => Promise<void>;
  t: (key: MessageKey, params?: MessageParams) => string;
};

/**
 * 注意 getNodeIdForRoot 是被 App 抽出去的：删项目要打数据面（跟选中节点走），
 * 而 worktree 永远在本机，所以两个删除函数里它出现与否不一样，不是漏写。
 */
export function useAppProjectRemoval(params: UseAppProjectRemovalParams) {
  const {
    clearRootScopedClientState, currentRootIdRef, currentRootNodeIdRef, getNodeIdForRoot,
    managedRootByIdRef, managedRootByKeyRef, refreshManagedRoots, t,
  } = params;

  /**
   * 按项目身份取 payload：显式节点 → 该节点的条目；否则回退唯一归属表。
   *
   * fn 与 pc 各有一个 `docs` 时，唯一归属表里没有它 —— 只查 byId 会拿到
   * undefined，删项目这类操作会直接报「找不到路径」。
   */
  const managedRootFor = useCallback((rootId: string, nodeId?: string | null): ManagedRootPayload | undefined => {
    const rid = String(rootId || "").trim();
    if (!rid) return undefined;
    const nid = String(nodeId || "").trim();
    if (nid) {
      const hit = (managedRootByKeyRef.current as Record<string, any>)[rootNodeKey(nid, rid)];
      if (hit) return hit as ManagedRootPayload;
    }
    return (managedRootByIdRef.current as Record<string, any>)[rid] as ManagedRootPayload | undefined;
  }, []);

  const findManagedRootByPath = useCallback((path: string, nodeId?: string): ManagedRootPayload | null => {
    const target = normalizeComparableRootPath(path);
    if (!target) {
      return null;
    }
    // 完整索引优先：同名/同路径项目在不同节点是不同项目，byId 里只剩唯一归属。
    const nid = String(nodeId || "").trim();
    const pool = Object.values(managedRootByKeyRef.current || {});
    const entries = pool.length > 0 ? pool : Object.values(managedRootByIdRef.current || {});
    for (const root of entries) {
      if (normalizeComparableRootPath(root.root_path || "") !== target) continue;
      if (nid && String((root as any)._nodeId || "").trim() !== nid) continue;
      return root;
    }
    return null;
  }, [normalizeComparableRootPath]);

  const handleRemoveCurrentRoot = useCallback(async () => {
    const rootID = currentRootIdRef.current;
    if (!rootID) {
      return;
    }
    // 当前项目按「当前选中节点」取：同名项目不能用唯一归属表碰运气
    const rootInfo = managedRootFor(rootID, currentRootNodeIdRef.current || getNodeIdForRoot(rootID));
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
  }, [clearRootScopedClientState, getNodeIdForRoot, managedRootFor, refreshManagedRoots, t]);

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
