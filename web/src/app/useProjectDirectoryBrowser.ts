import { useCallback, useState } from "react";
import { appPath, appURL } from "../services/net/base";
import { ProtectedAPIError, protectedJSON as apiProtectedJSON } from "../services/net/api";
import { getActiveNode, LOCAL_NODE_ID } from "../services/net/nodeRegistry";
import { promptDialog } from "../services/dialog";
import { managedDirAddErrorMessage, type LocalDirsPayload, type ManagedRootPayload } from "./appMisc";
import { useI18n } from "../i18n";
import type { GitHubImportState, LocalDirBrowserState, LocalDirItem, ProjectAddMode } from "../shared/projectAddTypes";

type OpenDirPayload = { path: string; root: string; isRoot: boolean; forceDirectory?: boolean };

export function useProjectDirectoryBrowser({
  currentRootIdRef,
  managedRootByIdRef,
  projectAddMode,
  setProjectAddMode,
  projectAddNodeId,
  setGitHubImportState,
  refreshManagedRoots,
  openManagedDir,
  handleCreateRootStart,
  handleCreateWorktreeStart,
}: {
  currentRootIdRef: React.MutableRefObject<string | null>;
  managedRootByIdRef: React.MutableRefObject<Record<string, ManagedRootPayload>>;
  projectAddMode: ProjectAddMode | null;
  setProjectAddMode: (mode: ProjectAddMode | null) => void;
  projectAddNodeId: string;
  setGitHubImportState: React.Dispatch<React.SetStateAction<GitHubImportState>>;
  refreshManagedRoots: () => Promise<void>;
  openManagedDir: (payload: OpenDirPayload) => Promise<void>;
  handleCreateRootStart: (parentPath?: string | null) => void;
  handleCreateWorktreeStart: (parentPath: string) => void;
}) {
  const { t } = useI18n();
  const [localDirState, setLocalDirState] = useState<LocalDirBrowserState>({
    path: "",
    parent: "",
    volumes: [],
    items: [],
    loading: false,
    selectedPath: "",
    adding: false,
    error: "",
  });

  const inferParentPath = useCallback((absolutePath: string): string => {
    const trimmed = absolutePath.trim().replace(/[\\/]+$/, "");
    const parts = trimmed.split(/[\\/]/).filter(Boolean);
    if (parts.length <= 1) return "";
    if (/^[A-Za-z]:/.test(trimmed)) {
      const drive = parts[0];
      const rest = parts.slice(1, -1);
      return rest.length > 0 ? `${drive}\\${rest.join("\\")}` : `${drive}\\`;
    }
    if (trimmed.startsWith("/")) return `/${parts.slice(0, -1).join("/")}`;
    return parts.slice(0, -1).join("/");
  }, []);

  const loadLocalDirs = useCallback(async (path: string, nodeId?: string) => {
    const trimmed = String(path || "").trim();
    setLocalDirState((prev) => ({ ...prev, loading: true, error: "", path: trimmed, selectedPath: "" }));
    try {
      const params = trimmed ? new URLSearchParams({ path: trimmed }) : undefined;
      const targetNodeId = nodeId || projectAddNodeId || getActiveNode()?.id || LOCAL_NODE_ID;
      const payload = await apiProtectedJSON<LocalDirsPayload>(appURL("/api/local_dirs", params, targetNodeId));
      setLocalDirState({
        path: String(payload.path || trimmed),
        parent: String(payload.parent || ""),
        volumes: Array.isArray(payload.volumes) ? payload.volumes.map(mapLocalDirItem) : [],
        items: Array.isArray(payload.items) ? payload.items.map(mapLocalDirItem) : [],
        loading: false,
        selectedPath: "",
        adding: false,
        error: "",
      });
    } catch (error) {
      setLocalDirState((prev) => ({
        ...prev,
        loading: false,
        error: error instanceof Error ? error.message : t("directory.loadFailed"),
      }));
    }
  }, [projectAddNodeId, t]);

  const openDirectoryPicker = useCallback((nextMode: ProjectAddMode) => {
    const rootID = currentRootIdRef.current;
    const rootPath = rootID ? String(managedRootByIdRef.current[rootID]?.root_path || "") : "";
    const initialPath = rootPath ? inferParentPath(rootPath) : "";
    setProjectAddMode(nextMode);
    if (!initialPath || initialPath === ".") {
      if (!rootPath) {
        void loadLocalDirs("");
        return;
      }
      setLocalDirState((prev) => ({
        ...prev,
        path: rootPath,
        parent: "",
        items: [],
        loading: false,
        selectedPath: "",
        adding: false,
        error: t("directory.noBrowsableParent"),
      }));
      return;
    }
    void loadLocalDirs(initialPath);
  }, [currentRootIdRef, inferParentPath, loadLocalDirs, managedRootByIdRef, setProjectAddMode, t]);

  const handleLocalDirSelect = useCallback((path: string) => {
    setLocalDirState((prev) => {
      const target = prev.items.find((item) => item.path === path);
      if (!target || (projectAddMode === "local" && target.is_added_root)) return prev;
      return { ...prev, selectedPath: path };
    });
  }, [projectAddMode]);

  const localPath = localDirState.path;
  const selectedPath = localDirState.selectedPath;
  const adding = localDirState.adding;
  const creating = localDirState.creating === true;

  /**
   * 在当前浏览目录下就地新建文件夹，建完继续导入。
   *
   * 两条收尾按模式分开：加入本地目录（"local"）是要把某个已存在目录登记成项目，
   * 所以建完把新目录设为 **选中**，用户再点「添加」；空白项目 / GitHub / worktree
   * 的按钮语义是「放置于此目录」，所以直接 **进入** 新目录，让后续动作落在它上面。
   */
  const handleLocalDirCreate = useCallback(async () => {
    const parentPath = String(localPath || "").trim();
    if (!parentPath || creating) return;
    const input = await promptDialog({ message: t("projectAdd.newFolderPrompt") });
    if (input === null) return;
    const name = input.trim();
    if (!name || name === "." || name === ".." || /[/\\\x00]/.test(name)) {
      setLocalDirState((prev) => ({ ...prev, error: t("directory.invalidFileName") }));
      return;
    }
    setLocalDirState((prev) => ({ ...prev, creating: true, error: "" }));
    const targetCreateNodeId = projectAddNodeId || getActiveNode()?.id || LOCAL_NODE_ID;
    try {
      const payload = await apiProtectedJSON<{ path?: string }>(
        appURL("/api/local_dirs", undefined, targetCreateNodeId),
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ path: parentPath, name }),
        },
      );
      const createdPath = String(payload?.path || "").trim();
      if (!createdPath) throw new Error(t("projectAdd.newFolderFailed"));
      if (projectAddMode === "local") {
        // 加入本地目录：留在父目录，把新目录插进列表并选中，交给「添加」。
        const item: LocalDirItem = {
          name,
          path: createdPath,
          is_dir: true,
          is_added_root: false,
          root_id: "",
        };
        setLocalDirState((prev) => ({
          ...prev,
          creating: false,
          selectedPath: createdPath,
          items: [...prev.items, item].sort((a, b) => a.name.localeCompare(b.name)),
        }));
        return;
      }
      // 其余模式：进入新目录，「放置于此目录」即落在它上面。
      setLocalDirState((prev) => ({ ...prev, creating: false }));
      void loadLocalDirs(createdPath, targetCreateNodeId);
    } catch (error) {
      setLocalDirState((prev) => ({
        ...prev,
        creating: false,
        error: error instanceof ProtectedAPIError && error.status === 409
          ? t("directory.fileExists")
          : String((error as Error)?.message || t("projectAdd.newFolderFailed")),
      }));
    }
  }, [
    creating, loadLocalDirs, localPath, projectAddMode, projectAddNodeId, t,
  ]);

  const handleLocalDirAdd = useCallback(async () => {
    const path = String(selectedPath || "").trim();
    if (adding) return;
    if (projectAddMode === "blank_location") {
      setProjectAddMode(null);
      handleCreateRootStart(localPath);
      return;
    }
    if (projectAddMode === "github_location") {
      setProjectAddMode("github");
      setGitHubImportState((prev) => ({
        ...prev,
        parentPath: localPath,
        taskId: "",
        status: "",
        message: "",
        running: false,
        submitting: false,
        done: false,
        error: "",
      }));
      return;
    }
    if (projectAddMode === "worktree_location") {
      setProjectAddMode(null);
      handleCreateWorktreeStart(localPath);
      return;
    }
    if (!path) return;
    setLocalDirState((prev) => ({ ...prev, adding: true, error: "" }));
    try {
      const targetAddNodeId = projectAddNodeId || getActiveNode()?.id || LOCAL_NODE_ID;
      const payload = await apiProtectedJSON<ManagedRootPayload>(appPath("/api/dirs", targetAddNodeId), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path, create: false }),
      });
      setLocalDirState((prev) => ({ ...prev, adding: false, selectedPath: "" }));
      setProjectAddMode(null);
      await refreshManagedRoots();
      if (payload?.id) await openManagedDir({ path: payload.id, root: payload.id, isRoot: true });
      void loadLocalDirs(localPath);
    } catch (error) {
      setLocalDirState((prev) => ({
        ...prev,
        adding: false,
        error: managedDirAddErrorMessage(error, t("root.addDirectoryFailed"), t),
      }));
    }
  }, [
    adding, handleCreateRootStart, handleCreateWorktreeStart, loadLocalDirs, localPath, selectedPath,
    openManagedDir, projectAddMode, projectAddNodeId, refreshManagedRoots,
    setGitHubImportState, setProjectAddMode, t,
  ]);

  return { localDirState, setLocalDirState, loadLocalDirs, openDirectoryPicker, handleLocalDirSelect, handleLocalDirAdd, handleLocalDirCreate };
}

function mapLocalDirItem(item: NonNullable<LocalDirsPayload["items"]>[number]) {
  return {
    name: String(item.name || ""),
    path: String(item.path || ""),
    is_dir: item.is_dir !== false,
    is_added_root: item.is_added_root === true,
    root_id: String(item.root_id || ""),
  };
}
