import { useCallback, useState } from "react";
import { appPath, appURL } from "../services/net/base";
import { protectedJSON as apiProtectedJSON } from "../services/net/api";
import { getActiveNode, LOCAL_NODE_ID } from "../services/net/nodeRegistry";
import { managedDirAddErrorMessage, type LocalDirsPayload, type ManagedRootPayload } from "./appMisc";
import { useI18n } from "../i18n";
import type { GitHubImportState, LocalDirBrowserState, ProjectAddMode } from "../shared/projectAddTypes";

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

  return { localDirState, setLocalDirState, loadLocalDirs, openDirectoryPicker, handleLocalDirSelect, handleLocalDirAdd };
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
