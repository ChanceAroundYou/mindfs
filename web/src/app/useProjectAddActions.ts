import { useCallback } from "react";
import { appPath } from "../services/net/base";
import { protectedJSON as apiProtectedJSON } from "../services/net/api";
import { getActiveNode, LOCAL_NODE_ID } from "../services/net/nodeRegistry";
import { useI18n } from "../i18n";
import type { GitHubImportState, ProjectAddMode } from "../shared/projectAddTypes";

export function useProjectAddActions({
  creatingRootBusy,
  projectAddNodeId,
  setProjectAddNodeId,
  setProjectAddMode,
  setGitHubImportState,
  openDirectoryPicker,
  setWorktreeSwitchOpen,
  handleCreateRootStart,
}: {
  creatingRootBusy: boolean;
  projectAddNodeId: string;
  setProjectAddNodeId: (id: string) => void;
  setProjectAddMode: (mode: ProjectAddMode | null) => void;
  setGitHubImportState: React.Dispatch<React.SetStateAction<GitHubImportState>>;
  openDirectoryPicker: (mode: ProjectAddMode) => void;
  setWorktreeSwitchOpen: (open: boolean) => void;
  handleCreateRootStart: (parentPath?: string | null) => void;
}) {
  const { t } = useI18n();

  const handleOpenProjectAdd = useCallback(() => {
    if (!creatingRootBusy) setProjectAddMode("mode");
  }, [creatingRootBusy, setProjectAddMode]);

  const handleOpenLocalProjectAdd = useCallback(() => {
    try { const node = getActiveNode(); if (node?.id) setProjectAddNodeId(node.id); } catch {}
    openDirectoryPicker("local");
  }, [openDirectoryPicker, setProjectAddNodeId]);

  const handleOpenBlankProjectLocation = useCallback(() => {
    openDirectoryPicker("blank_location");
  }, [openDirectoryPicker]);

  const handleOpenGitHubProjectAdd = useCallback(() => {
    openDirectoryPicker("github_location");
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
  }, [openDirectoryPicker, setGitHubImportState]);

  const handleOpenWorktreeLocation = useCallback(() => {
    setWorktreeSwitchOpen(false);
    openDirectoryPicker("worktree_location");
  }, [openDirectoryPicker, setWorktreeSwitchOpen]);

  const handleSelectBlankProject = useCallback(() => {
    setProjectAddMode(null);
    handleCreateRootStart();
  }, [handleCreateRootStart, setProjectAddMode]);

  const handleGitHubImportStart = useCallback(async (state: GitHubImportState) => {
    const url = String(state.url || "").trim();
    const parentPath = String(state.parentPath || "").trim();
    if (!url || !parentPath || state.running || state.submitting) return;
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
  }, [projectAddNodeId, setGitHubImportState, t]);

  return {
    handleOpenProjectAdd,
    handleOpenLocalProjectAdd,
    handleOpenBlankProjectLocation,
    handleOpenGitHubProjectAdd,
    handleOpenWorktreeLocation,
    handleSelectBlankProject,
    handleGitHubImportStart,
  };
}
