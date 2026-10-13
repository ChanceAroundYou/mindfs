import { useCallback, useEffect, useRef, useState } from "react";
import {
  createGitWorktree,
  fetchGitBranches,
  fetchGitWorktrees,
  type GitBranchesPayload,
  type GitWorktreeItem,
} from "../services/git";
import { appPath } from "../services/net/base";
import { protectedJSON as apiProtectedJSON } from "../services/net/api";
import { reportError } from "../services/net/error";
import { getActiveNode, LOCAL_NODE_ID } from "../services/net/nodeRegistry";
import { managedDirAddErrorMessage, type ManagedRootPayload } from "./appMisc";
import { ProjectAddLifecycleOverlay } from "../components/file/ProjectAddPopover";
import type { GitHubImportState, ProjectAddMode } from "../shared/projectAddTypes";
import { useI18n } from "../i18n";
import { WorktreeLifecycleOverlays } from "../components/file/WorktreeLifecycleOverlays";
import { useProjectDirectoryBrowser } from "./useProjectDirectoryBrowser";
import { useProjectAddActions } from "./useProjectAddActions";

type OpenDirPayload = { path: string; root: string; isRoot: boolean; forceDirectory?: boolean };

/**
 * 项目管理生命周期：新建/添加项目、创建 worktree、切换 worktree。
 *
 * 三块状态各自独立但共用一套「弹层 + 点外关闭」节奏，所以放一起：
 * - projectAdd*：选路径 → 加项目 / 建目录 / 建 worktree 的目录选择器
 * - creatingRoot*：命名并提交（root 或 worktree）
 * - worktreeSwitch*：在已有 worktree 之间切换（不在列表里的现场注册成项目）
 */
export function useProjectLifecycle({
  currentRootId,
  currentRootIdRef,
  getNodeIdForRoot,
  refreshManagedRoots,
  findManagedRootByPath,
  managedRootIdsRef,
  managedRootByIdRef,
  openManagedDir,
}: {
  currentRootId: string | null;
  currentRootIdRef: React.MutableRefObject<string | null>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  refreshManagedRoots: () => Promise<void>;
  findManagedRootByPath: (path: string) => ManagedRootPayload | null;
  managedRootIdsRef: React.MutableRefObject<Set<string>>;
  managedRootByIdRef: React.MutableRefObject<Record<string, ManagedRootPayload>>;
  openManagedDir: (payload: OpenDirPayload) => Promise<void>;
}) {
  const { t } = useI18n();
  const projectAddPopoverRef = useRef<HTMLDivElement | null>(null);
  const worktreeCreatePopoverRef = useRef<HTMLDivElement | null>(null);
  const worktreeSwitchPopoverRef = useRef<HTMLDivElement | null>(null);

  const [creatingRootName, setCreatingRootName] = useState<string | null>(null);
  const [creatingRootParentPath, setCreatingRootParentPath] = useState<string | null>(null);
  const [creatingRootKind, setCreatingRootKind] = useState<"root" | "worktree">("root");
  const [creatingRootBusy, setCreatingRootBusy] = useState(false);
  const [worktreeBranches, setWorktreeBranches] = useState<GitBranchesPayload>({ branches: [] });
  const [worktreeBranchesLoading, setWorktreeBranchesLoading] = useState(false);
  const [worktreeBranchError, setWorktreeBranchError] = useState("");
  const [worktreeBranchMode, setWorktreeBranchMode] = useState<"new" | "existing">("new");
  const [worktreeBranch, setWorktreeBranch] = useState("");
  const [worktreeSwitchOpen, setWorktreeSwitchOpen] = useState(false);
  const [worktreeSwitchItems, setWorktreeSwitchItems] = useState<GitWorktreeItem[]>([]);
  const [worktreeSwitchLoading, setWorktreeSwitchLoading] = useState(false);
  const [worktreeSwitchError, setWorktreeSwitchError] = useState("");
  const [switchingWorktreePath, setSwitchingWorktreePath] = useState("");
  const [projectAddMode, setProjectAddMode] = useState<ProjectAddMode | null>(null);
  const [projectAddNodeId, setProjectAddNodeId] = useState<string>(() => {
    try { const n = getActiveNode(); return n?.id || LOCAL_NODE_ID; } catch { return LOCAL_NODE_ID; }
  });
  const [githubImportState, setGitHubImportState] = useState<GitHubImportState>({
    url: "",
    parentPath: "",
    taskId: "",
    status: "",
    message: "",
    running: false,
    submitting: false,
    done: false,
    error: "",
  });

  useEffect(() => {
    if (!projectAddMode) {
      return;
    }
    const handlePointerDown = (event: MouseEvent) => {
      if (!projectAddPopoverRef.current?.contains(event.target as Node)) {
        setProjectAddMode(null);
      }
    };
    document.addEventListener("mousedown", handlePointerDown);
    return () => document.removeEventListener("mousedown", handlePointerDown);
  }, [projectAddMode]);

  const loadWorktreeBranches = useCallback(async (rootID: string) => {
    setWorktreeBranchesLoading(true);
    setWorktreeBranchError("");
    try {
      const payload = await fetchGitBranches(rootID, getNodeIdForRoot(rootID));
      setWorktreeBranches(payload);
    } catch (error) {
      setWorktreeBranches({ branches: [] });
      setWorktreeBranchError(error instanceof Error ? error.message : t("worktree.loadBranchFailed"));
    } finally {
      setWorktreeBranchesLoading(false);
    }
  }, [getNodeIdForRoot, t]);

  const loadWorktreeList = useCallback(async (rootID: string) => {
    setWorktreeSwitchLoading(true);
    setWorktreeSwitchError("");
    try {
      const payload = await fetchGitWorktrees(rootID, getNodeIdForRoot(rootID));
      setWorktreeSwitchItems(payload.items || []);
    } catch (error) {
      setWorktreeSwitchItems([]);
      setWorktreeSwitchError(error instanceof Error ? error.message : t("worktree.loadFailed"));
    } finally {
      setWorktreeSwitchLoading(false);
    }
  }, [getNodeIdForRoot, t]);

  const handleCreateRootStart = useCallback((parentPath?: string | null) => {
    if (creatingRootBusy) {
      return;
    }
    const existing = new Set(managedRootIdsRef.current);
    let nextName = "new-root";
    let suffix = 2;
    while (existing.has(nextName)) {
      nextName = `new-root-${suffix}`;
      suffix += 1;
    }
    setCreatingRootParentPath(
      parentPath && String(parentPath).trim() ? String(parentPath).trim() : null,
    );
    setCreatingRootKind("root");
    setCreatingRootName(nextName);
  }, [creatingRootBusy, managedRootIdsRef]);

  const handleCreateWorktreeStart = useCallback((parentPath: string) => {
    if (creatingRootBusy) {
      return;
    }
    const rootID = currentRootIdRef.current;
    if (!rootID) {
      return;
    }
    const existing = new Set(managedRootIdsRef.current);
    const baseName = `${rootID}-worktree`;
    let nextName = baseName;
    let suffix = 2;
    while (existing.has(nextName)) {
      nextName = `${baseName}-${suffix}`;
      suffix += 1;
    }
    setCreatingRootKind("worktree");
    setCreatingRootParentPath(parentPath);
    setCreatingRootName(nextName);
    setWorktreeBranchMode("new");
    setWorktreeBranch("");
    setWorktreeBranches({ branches: [] });
    setWorktreeBranchError("");
    void loadWorktreeBranches(rootID);
  }, [creatingRootBusy, currentRootIdRef, loadWorktreeBranches, managedRootIdsRef]);

  const {
    localDirState,
    loadLocalDirs,
    openDirectoryPicker,
    handleLocalDirSelect,
    handleLocalDirAdd,
  } = useProjectDirectoryBrowser({
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
  });

  const {
    handleOpenProjectAdd,
    handleOpenLocalProjectAdd,
    handleOpenBlankProjectLocation,
    handleOpenGitHubProjectAdd,
    handleOpenWorktreeLocation,
    handleSelectBlankProject,
    handleGitHubImportStart,
  } = useProjectAddActions({
    creatingRootBusy,
    projectAddNodeId,
    setProjectAddNodeId,
    setProjectAddMode,
    setGitHubImportState,
    openDirectoryPicker,
    setWorktreeSwitchOpen,
    handleCreateRootStart,
  });

  const handleSwitchWorktreeStart = useCallback(() => {
    const rootID = currentRootIdRef.current;
    if (!rootID) {
      return;
    }
    setProjectAddMode(null);
    setCreatingRootName(null);
    setWorktreeSwitchOpen(true);
    setWorktreeSwitchItems([]);
    setWorktreeSwitchError("");
    void loadWorktreeList(rootID);
  }, [currentRootIdRef, loadWorktreeList]);

  const handleSwitchWorktree = useCallback(async (item: GitWorktreeItem) => {
    const targetPath = String(item.path || "").trim();
    if (!targetPath || switchingWorktreePath) {
      return;
    }
    setSwitchingWorktreePath(targetPath);
    try {
      let targetRoot = findManagedRootByPath(targetPath);
      if (!targetRoot?.id) {
        const created = await apiProtectedJSON<ManagedRootPayload>(appPath("/api/dirs"), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ path: targetPath, create: false }),
        });
        targetRoot = created;
        await refreshManagedRoots();
      }
      if (targetRoot?.id) {
        setWorktreeSwitchOpen(false);
        await openManagedDir({
          path: targetRoot.id,
          root: targetRoot.id,
          isRoot: true,
          forceDirectory: true,
        });
        return targetRoot.id;
      }
    } catch (error) {
      reportError(
        "git.worktree_switch_failed",
        managedDirAddErrorMessage(error, t("root.switchWorktreeFailed"), t),
      );
    } finally {
      setSwitchingWorktreePath("");
    }
    return undefined;
  }, [findManagedRootByPath, openManagedDir, refreshManagedRoots, switchingWorktreePath, t]);

  const handleCreateRootCancel = useCallback(() => {
    if (creatingRootBusy) {
      return;
    }
    setCreatingRootName(null);
    setCreatingRootParentPath(null);
    setCreatingRootKind("root");
    setWorktreeBranchMode("new");
    setWorktreeBranch("");
    setWorktreeBranchError("");
    setWorktreeSwitchOpen(false);
  }, [creatingRootBusy]);

  useEffect(() => {
    if (creatingRootKind !== "worktree" || creatingRootName === null) {
      return;
    }
    const handlePointerDown = (event: MouseEvent) => {
      if (worktreeCreatePopoverRef.current?.contains(event.target as Node)) {
        return;
      }
      handleCreateRootCancel();
    };
    document.addEventListener("mousedown", handlePointerDown);
    return () => document.removeEventListener("mousedown", handlePointerDown);
  }, [creatingRootKind, creatingRootName, handleCreateRootCancel]);

  useEffect(() => {
    if (!worktreeSwitchOpen) {
      return;
    }
    const handlePointerDown = (event: MouseEvent) => {
      if (worktreeSwitchPopoverRef.current?.contains(event.target as Node)) {
        return;
      }
      setWorktreeSwitchOpen(false);
    };
    document.addEventListener("mousedown", handlePointerDown);
    return () => document.removeEventListener("mousedown", handlePointerDown);
  }, [worktreeSwitchOpen]);

  const handleCreateRootSubmit = useCallback(async () => {
    const name = String(creatingRootName || "").trim();
    if (!name) {
      setCreatingRootName(null);
      setCreatingRootParentPath(null);
      return;
    }
    if (creatingRootBusy) {
      return;
    }
    setCreatingRootBusy(true);
    try {
      if (creatingRootKind === "worktree") {
        const rootID = currentRootIdRef.current;
        const parentPath = String(creatingRootParentPath || "").trim();
        if (!rootID || !parentPath) {
          throw new Error(t("worktree.createLocationMissing"));
        }
        const created = await createGitWorktree({
          rootId: rootID,
          parentPath,
          name,
          branchMode: worktreeBranchMode,
          branch: worktreeBranchMode === "existing" ? worktreeBranch : "",
        }) as ManagedRootPayload;
        setCreatingRootName(null);
        setCreatingRootParentPath(null);
        setCreatingRootKind("root");
        setWorktreeBranchMode("new");
        setWorktreeBranch("");
        await refreshManagedRoots();
        if (created?.id) {
          await openManagedDir({ path: created.id, root: created.id, isRoot: true });
        }
        return;
      }
      const targetPath =
        creatingRootParentPath && creatingRootParentPath.trim()
          ? `${creatingRootParentPath.replace(/[\\/]+$/, "")}/${name}`
          : name;
      const targetCreateNodeId = projectAddNodeId || getActiveNode()?.id || LOCAL_NODE_ID;
      const payload = await apiProtectedJSON<any>(appPath("/api/dirs", targetCreateNodeId), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: targetPath, create: true }),
      });
      const created = payload as ManagedRootPayload;
      setCreatingRootName(null);
      setCreatingRootParentPath(null);
      await refreshManagedRoots();
      if (created?.id) {
        await openManagedDir({ path: created.id, root: created.id, isRoot: true });
      }
    } catch (err) {
      reportError(
        "root.create_failed",
        managedDirAddErrorMessage(err, t("root.createProjectFailed"), t),
      );
    } finally {
      setCreatingRootBusy(false);
    }
  }, [
    creatingRootBusy,
    creatingRootKind,
    creatingRootName,
    creatingRootParentPath,
    currentRootIdRef,
    openManagedDir,
    projectAddNodeId,
    refreshManagedRoots,
    t,
    worktreeBranch,
    worktreeBranchMode,
  ]);

  const projectAddOverlay = projectAddMode ? (
    <ProjectAddLifecycleOverlay
      popoverRef={projectAddPopoverRef}
      mode={projectAddMode}
      setMode={setProjectAddMode}
      onSelectLocal={handleOpenLocalProjectAdd}
      onSelectBlankLocation={handleOpenBlankProjectLocation}
      onSelectGitHubLocation={handleOpenGitHubProjectAdd}
      onSelectBlank={handleSelectBlankProject}
      localState={localDirState}
      selectedNodeId={projectAddNodeId}
      onSelectedNodeChange={(id) => {
        setProjectAddNodeId(id);
        void loadLocalDirs("", id);
      }}
      onLocalNavigate={(path) => void loadLocalDirs(path, projectAddNodeId)}
      onLocalSelect={handleLocalDirSelect}
      onLocalAdd={() => void handleLocalDirAdd()}
      githubState={githubImportState}
      onGitHubUrlChange={(value) => setGitHubImportState((prev) => ({
        ...prev,
        url: value,
        error: "",
        done: false,
      }))}
      onGitHubImport={() => void handleGitHubImportStart(githubImportState)}
    />
  ) : null;

  const worktreeOverlayProps = {
    createPopoverRef: worktreeCreatePopoverRef,
    switchPopoverRef: worktreeSwitchPopoverRef,
    creatingRootKind,
    creatingRootName,
    creatingRootBusy,
    setCreatingRootName,
    handleCreateRootSubmit,
    handleCreateRootCancel,
    worktreeBranches,
    worktreeBranchesLoading,
    worktreeBranchError,
    worktreeBranchMode,
    setWorktreeBranchMode,
    worktreeBranch,
    setWorktreeBranch,
    worktreeSwitchOpen,
    worktreeSwitchLoading,
    worktreeSwitchError,
    worktreeSwitchItems,
    switchingWorktreePath,
    currentRootId,
    findManagedRootByPath,
    handleSwitchWorktree,
  };
  const worktreeCreateOverlay = <WorktreeLifecycleOverlays {...worktreeOverlayProps} mode="create" />;
  const worktreeSwitchOverlay = <WorktreeLifecycleOverlays {...worktreeOverlayProps} mode="switch" />;

  return {
    projectAddOverlay,
    worktreeCreateOverlay,
    worktreeSwitchOverlay,
    projectAddPopoverRef,
    worktreeCreatePopoverRef,
    worktreeSwitchPopoverRef,
    creatingRootName,
    setCreatingRootName,
    creatingRootParentPath,
    creatingRootKind,
    creatingRootBusy,
    worktreeBranches,
    worktreeBranchesLoading,
    worktreeBranchError,
    worktreeBranchMode,
    setWorktreeBranchMode,
    worktreeBranch,
    setWorktreeBranch,
    worktreeSwitchOpen,
    setWorktreeSwitchOpen,
    worktreeSwitchItems,
    worktreeSwitchLoading,
    worktreeSwitchError,
    switchingWorktreePath,
    projectAddMode,
    setProjectAddMode,
    projectAddNodeId,
    setProjectAddNodeId,
    localDirState,
    githubImportState,
    setGitHubImportState,
    handleCreateRootStart,
    handleCreateWorktreeStart,
    handleSwitchWorktreeStart,
    handleSwitchWorktree,
    handleOpenProjectAdd,
    loadLocalDirs,
    openDirectoryPicker,
    handleOpenLocalProjectAdd,
    handleOpenBlankProjectLocation,
    handleOpenGitHubProjectAdd,
    handleOpenWorktreeLocation,
    handleSelectBlankProject,
    handleCreateRootCancel,
    handleCreateRootSubmit,
    handleLocalDirSelect,
    handleLocalDirAdd,
    handleGitHubImportStart,
  };
}
