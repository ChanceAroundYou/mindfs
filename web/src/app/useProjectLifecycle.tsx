import { useCallback, useEffect, useRef, useState } from "react";
import {
  createGitWorktree,
  fetchGitBranches,
  fetchGitWorktrees,
  type GitBranchesPayload,
  type GitWorktreeItem,
} from "../services/git";
import { appPath, appURL } from "../services/base";
import { protectedJSON as apiProtectedJSON } from "../services/api";
import { reportError } from "../services/error";
import { getActiveNode, LOCAL_NODE_ID } from "../services/nodeRegistry";
import { managedDirAddErrorMessage, type LocalDirsPayload, type ManagedRootPayload } from "./appMisc";
import {
  ProjectAddPopover,
  type GitHubImportState,
  type LocalDirBrowserState,
  type ProjectAddMode,
} from "../components/ProjectAddPopover";
import { useI18n } from "../i18n";

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

  const handleOpenProjectAdd = useCallback(() => {
    if (creatingRootBusy) {
      return;
    }
    setProjectAddMode("mode");
  }, [creatingRootBusy]);

  const inferParentPath = useCallback((absolutePath: string): string => {
    const trimmed = absolutePath.trim().replace(/[\\/]+$/, "");
    const parts = trimmed.split(/[\\/]/).filter(Boolean);
    if (parts.length <= 1) {
      return "";
    }
    if (/^[A-Za-z]:/.test(trimmed)) {
      const drive = parts[0];
      const rest = parts.slice(1, -1);
      return rest.length > 0 ? `${drive}\\${rest.join("\\")}` : `${drive}\\`;
    }
    if (trimmed.startsWith("/")) {
      return `/${parts.slice(0, -1).join("/")}`;
    }
    return parts.slice(0, -1).join("/");
  }, []);

  const loadLocalDirs = useCallback(async (path: string, nodeId?: string) => {
    const trimmed = String(path || "").trim();
    setLocalDirState((prev) => ({
      ...prev,
      loading: true,
      error: "",
      path: trimmed,
      selectedPath: "",
    }));
    try {
      const params = trimmed ? new URLSearchParams({ path: trimmed }) : undefined;
      const targetNodeId = (nodeId || projectAddNodeId || getActiveNode()?.id || LOCAL_NODE_ID);
      const payload = await apiProtectedJSON<LocalDirsPayload>(appURL("/api/local_dirs", params, targetNodeId));
      setLocalDirState({
        path: String(payload.path || trimmed),
        parent: String(payload.parent || ""),
        volumes: Array.isArray(payload.volumes)
          ? payload.volumes.map((item) => ({
              name: String(item.name || ""),
              path: String(item.path || ""),
              is_dir: item.is_dir !== false,
              is_added_root: item.is_added_root === true,
              root_id: String(item.root_id || ""),
            }))
          : [],
        items: Array.isArray(payload.items)
          ? payload.items.map((item) => ({
              name: String(item.name || ""),
              path: String(item.path || ""),
              is_dir: item.is_dir !== false,
              is_added_root: item.is_added_root === true,
              root_id: String(item.root_id || ""),
            }))
          : [],
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
    const rootPath = rootID
      ? String(managedRootByIdRef.current[rootID]?.root_path || "")
      : "";
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
  }, [currentRootIdRef, inferParentPath, loadLocalDirs, managedRootByIdRef, t]);

  const handleOpenLocalProjectAdd = useCallback(() => {
    // reset to active/local when opening
    try { const n = getActiveNode(); if (n?.id) setProjectAddNodeId(n.id); } catch {}
    void openDirectoryPicker("local");
  }, [openDirectoryPicker]);

  const handleOpenBlankProjectLocation = useCallback(() => {
    void openDirectoryPicker("blank_location");
  }, [openDirectoryPicker]);

  const handleOpenGitHubProjectAdd = useCallback(() => {
    void openDirectoryPicker("github_location");
    setGitHubImportState((prev) => ({
      ...prev,
      parentPath: "",
      taskId: "",
      status: "",
      message: "",
      running: false,
      submitting: false,
      done: false,
      error: "",
    }));
  }, [openDirectoryPicker]);

  const handleOpenWorktreeLocation = useCallback(() => {
    setWorktreeSwitchOpen(false);
    void openDirectoryPicker("worktree_location");
  }, [openDirectoryPicker]);

  const handleSelectBlankProject = useCallback(() => {
    setProjectAddMode(null);
    handleCreateRootStart();
  }, [handleCreateRootStart]);

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

  const handleLocalDirSelect = useCallback((path: string) => {
    setLocalDirState((prev) => {
      const target = prev.items.find((item) => item.path === path);
      if (!target) {
        return prev;
      }
      if (projectAddMode === "local" && target.is_added_root) {
        return prev;
      }
      return { ...prev, selectedPath: path };
    });
  }, [projectAddMode]);

  const handleLocalDirAdd = useCallback(async () => {
    const path = String(localDirState.selectedPath || "").trim();
    if (localDirState.adding) {
      return;
    }
    if (projectAddMode === "blank_location") {
      setProjectAddMode(null);
      handleCreateRootStart(localDirState.path);
      return;
    }
    if (projectAddMode === "github_location") {
      setProjectAddMode("github");
      setGitHubImportState((prev) => ({
        ...prev,
        parentPath: localDirState.path,
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
      handleCreateWorktreeStart(localDirState.path);
      return;
    }
    if (!path) {
      return;
    }
    setLocalDirState((prev) => ({ ...prev, adding: true, error: "" }));
    try {
      const targetAddNodeId = projectAddNodeId || getActiveNode()?.id || LOCAL_NODE_ID;
      const payload = await apiProtectedJSON<any>(appPath("/api/dirs", targetAddNodeId), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path, create: false }),
      });
      setLocalDirState((prev) => ({
        ...prev,
        adding: false,
        selectedPath: "",
      }));
      setProjectAddMode(null);
      await refreshManagedRoots();
      const created = payload as ManagedRootPayload;
      if (created?.id) {
        await openManagedDir({ path: created.id, root: created.id, isRoot: true });
      }
      void loadLocalDirs(localDirState.path);
    } catch (error) {
      setLocalDirState((prev) => ({
        ...prev,
        adding: false,
        error: managedDirAddErrorMessage(error, t("root.addDirectoryFailed"), t),
      }));
    }
  }, [handleCreateRootStart, handleCreateWorktreeStart, loadLocalDirs, localDirState.adding, localDirState.path, localDirState.selectedPath, openManagedDir, projectAddMode, projectAddNodeId, refreshManagedRoots, t]);

  const handleGitHubImportStart = useCallback(async () => {
    const url = String(githubImportState.url || "").trim();
    const parentPath = String(githubImportState.parentPath || "").trim();
    if (
      !url ||
      !parentPath ||
      githubImportState.running ||
      githubImportState.submitting
    ) {
      return;
    }
    setGitHubImportState((prev) => ({
      ...prev,
      submitting: true,
      done: false,
      error: "",
      taskId: "",
      status: "",
      message: "",
    }));
    try {
      const targetImportNodeId = projectAddNodeId || getActiveNode()?.id || LOCAL_NODE_ID;
      const payload = await apiProtectedJSON<any>(appPath("/api/imports/github", targetImportNodeId), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ url, parent_path: parentPath }),
      });
      setGitHubImportState((prev) => ({
        ...prev,
        taskId: String(payload?.task_id || ""),
        status: "pending",
        message: t("projectAdd.cloning"),
        submitting: false,
        running: true,
        done: false,
        error: "",
      }));
    } catch (error) {
      setGitHubImportState((prev) => ({
        ...prev,
        submitting: false,
        running: false,
        done: false,
        error: error instanceof Error ? error.message : t("root.githubImportFailed"),
      }));
    }
  }, [githubImportState.parentPath, githubImportState.running, githubImportState.submitting, githubImportState.url, projectAddNodeId, t]);

  const worktreeBranchSelector =
    creatingRootKind === "worktree" ? (
      <div style={{ display: "flex", flexDirection: "column", gap: "5px" }}>
        <select
          value={worktreeBranchMode === "new" ? "__new__" : worktreeBranch}
          disabled={creatingRootBusy}
          onChange={(event) => {
            const value = event.target.value;
            if (value === "__new__") {
              setWorktreeBranchMode("new");
              setWorktreeBranch("");
              return;
            }
            setWorktreeBranchMode("existing");
            setWorktreeBranch(value);
          }}
          style={{
            width: "100%",
            borderRadius: "7px",
            border: "1px solid var(--border-color)",
            background: "var(--menu-bg)",
            color: "var(--text-primary)",
            fontSize: "12px",
            padding: "6px 8px",
            outline: "none",
          }}
        >
          <option value="__new__">{t("worktree.createBranch")}</option>
          {worktreeBranches.branches.map((branch) => (
            <option key={branch.name} value={branch.name}>
              {branch.current ? `${branch.name} ${t("worktree.current")}` : branch.name}
            </option>
          ))}
        </select>
        {worktreeBranchesLoading ? (
          <span style={{ fontSize: "11px", color: "var(--text-secondary)" }}>{t("worktree.loadingBranches")}</span>
        ) : worktreeBranchError ? (
          <span style={{ fontSize: "11px", color: "#b45309" }}>{worktreeBranchError}</span>
        ) : null}
      </div>
    ) : null;

  const worktreeCreateOverlay =
    creatingRootKind === "worktree" && creatingRootName !== null ? (
      <div
        ref={worktreeCreatePopoverRef}
        style={{
          width: "248px",
          maxWidth: "calc(100vw - 32px)",
          padding: "10px",
          borderRadius: "12px",
          border: "1px solid var(--border-color)",
          background: "var(--menu-bg)",
          boxShadow: "0 12px 30px rgba(15, 23, 42, 0.14)",
          display: "flex",
          flexDirection: "column",
          gap: "10px",
        }}
      >
        <div style={{ fontSize: "12px", fontWeight: 600, color: "var(--text-primary)" }}>
          worktree
        </div>
        <input
          value={creatingRootName}
          disabled={creatingRootBusy}
          autoFocus
          onChange={(event) => setCreatingRootName(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              void handleCreateRootSubmit();
            } else if (event.key === "Escape") {
              event.preventDefault();
              handleCreateRootCancel();
            }
          }}
          style={{
            width: "100%",
            borderRadius: "8px",
            border: "1px solid var(--border-color)",
            background: "transparent",
            color: "var(--text-primary)",
            fontSize: "12px",
            padding: "8px 10px",
            outline: "none",
            boxSizing: "border-box",
          }}
        />
        {worktreeBranchSelector}
        <div style={{ display: "flex" }}>
          <button
            type="button"
            disabled={creatingRootBusy || !String(creatingRootName || "").trim()}
            onClick={() => {
              void handleCreateRootSubmit();
            }}
            style={{
              width: "100%",
              border: "none",
              background: creatingRootBusy || !String(creatingRootName || "").trim()
                ? "rgba(59, 130, 246, 0.65)"
                : "var(--accent-color)",
              color: "#fff",
              borderRadius: "8px",
              padding: "8px 10px",
              fontSize: "12px",
              fontWeight: 600,
              cursor: creatingRootBusy || !String(creatingRootName || "").trim()
                ? "not-allowed"
                : "pointer",
            }}
          >
            {creatingRootBusy ? t("worktree.processing") : t("worktree.create")}
          </button>
        </div>
      </div>
    ) : null;

  const worktreeSwitchOverlay =
    worktreeSwitchOpen ? (
      <div
        ref={worktreeSwitchPopoverRef}
        style={{
          width: "248px",
          maxWidth: "calc(100vw - 32px)",
          maxHeight: "360px",
          padding: "8px",
          borderRadius: "12px",
          border: "1px solid var(--border-color)",
          background: "var(--menu-bg)",
          boxShadow: "0 12px 30px rgba(15, 23, 42, 0.14)",
          display: "flex",
          flexDirection: "column",
          gap: "6px",
          overflow: "auto",
        }}
      >
        <div style={{ padding: "4px 6px 6px", fontSize: "12px", fontWeight: 600, color: "var(--text-primary)" }}>
          {t("worktree.switchTitle")}
        </div>
        {worktreeSwitchLoading ? (
          <div style={{ padding: "8px 6px", fontSize: "12px", color: "var(--text-secondary)" }}>{t("common.loading")}</div>
        ) : worktreeSwitchError ? (
          <div style={{ padding: "8px 6px", fontSize: "12px", color: "#b45309" }}>{worktreeSwitchError}</div>
        ) : worktreeSwitchItems.length === 0 ? (
          <div style={{ padding: "8px 6px", fontSize: "12px", color: "var(--text-secondary)" }}>{t("worktree.noSwitchable")}</div>
        ) : worktreeSwitchItems.map((item) => {
          const managed = findManagedRootByPath(item.path);
          const active = item.current || managed?.id === currentRootId;
          const busy = switchingWorktreePath === item.path;
          const name = String(item.path || "").replace(/[\\/]+$/, "").split(/[\\/]/).filter(Boolean).pop() || item.path;
          return (
            <button
              key={item.path}
              type="button"
              disabled={active || !!switchingWorktreePath}
              onClick={() => {
                void handleSwitchWorktree(item);
              }}
              style={{
                width: "100%",
                border: "none",
                background: active ? "var(--selection-bg)" : "transparent",
                color: active ? "var(--accent-color)" : "var(--text-primary)",
                borderRadius: "8px",
                padding: "8px 10px",
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                gap: "12px",
                textAlign: "left",
                cursor: active || switchingWorktreePath ? "default" : "pointer",
                opacity: switchingWorktreePath && !busy ? 0.56 : 1,
              }}
            >
              <span style={{ minWidth: 0, display: "flex", flexDirection: "column", gap: "2px" }}>
                <span style={{ fontSize: "12px", fontWeight: 600, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {name}
                </span>
                <span style={{ fontSize: "11px", color: "var(--text-secondary)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {item.branch || item.head?.slice(0, 8) || item.path}
                </span>
              </span>
              <span style={{ fontSize: "11px", color: active ? "var(--accent-color)" : "var(--text-secondary)", flexShrink: 0 }}>
                {busy ? "..." : active ? t("worktree.current") : managed ? t("worktree.switch") : t("worktree.join")}
              </span>
            </button>
          );
        })}
      </div>
    ) : null;

  const projectAddOverlay = projectAddMode ? (
    <div ref={projectAddPopoverRef}>
      <ProjectAddPopover
        mode={projectAddMode}
        onSelectMode={() => setProjectAddMode("mode")}
        onSelectLocal={handleOpenLocalProjectAdd}
        onSelectBlankLocation={handleOpenBlankProjectLocation}
        onSelectGitHubLocation={handleOpenGitHubProjectAdd}
        onSelectGitHub={handleOpenGitHubProjectAdd}
        onSelectBlank={handleSelectBlankProject}
        localState={localDirState}
        selectedNodeId={projectAddNodeId}
        onSelectedNodeChange={(id) => {
          setProjectAddNodeId(id);
          void loadLocalDirs("", id);
        }}
        onLocalNavigate={(path) => {
          void loadLocalDirs(path, projectAddNodeId);
        }}
        onLocalSelect={handleLocalDirSelect}
        onLocalAdd={() => {
          void handleLocalDirAdd();
        }}
        localActionLabel={
          projectAddMode === "local" ? t("projectAdd.add") : t("projectAdd.placeHere")
        }
        localDisabledAddedRoot={projectAddMode === "local"}
        localBrowseOnly={
          projectAddMode === "blank_location" ||
          projectAddMode === "github_location" ||
          projectAddMode === "worktree_location"
        }
        githubState={githubImportState}
        onGitHubUrlChange={(value) =>
          setGitHubImportState((prev) => ({
            ...prev,
            url: value,
            error: "",
            done: false,
          }))
        }
        onGitHubImport={() => {
          void handleGitHubImportStart();
        }}
      />
    </div>
  ) : null;

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
