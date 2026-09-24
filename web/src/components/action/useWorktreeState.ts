import { useEffect, useState } from "react";
import { fetchGitBranches, type GitBranchesPayload } from "../../services/git";
import { useI18n } from "../../i18n";
import { type SessionInfo } from "./types";

/** 新会话 worktree 选项：开关 + 分支模式/分支，分支列表按需拉取。 */
export function useWorktreeState({
  currentRootId,
  currentRootIsGitRepo,
  currentSession,
  mode,
}: {
  currentRootId?: string | null;
  currentRootIsGitRepo: boolean;
  currentSession?: SessionInfo | null;
  mode: string;
}) {
  const { t } = useI18n();
  const [createWorktree, setCreateWorktree] = useState(false);
  const [worktreeBranchMode, setWorktreeBranchMode] = useState<"new" | "existing">("new");
  const [worktreeBranch, setWorktreeBranch] = useState("");
  const [worktreeBranches, setWorktreeBranches] = useState<GitBranchesPayload>({ branches: [] });
  const [worktreeBranchesLoading, setWorktreeBranchesLoading] = useState(false);
  const [worktreeBranchError, setWorktreeBranchError] = useState("");

  useEffect(() => {
    if (!currentSession) {
      return;
    }
    setCreateWorktree(false);
    setWorktreeBranchMode("new");
    setWorktreeBranch("");
  }, [currentSession?.key, currentSession?.session_key, currentSession?.pending]);

  useEffect(() => {
    setCreateWorktree(false);
    setWorktreeBranchMode("new");
    setWorktreeBranch("");
    setWorktreeBranches({ branches: [] });
    setWorktreeBranchError("");
  }, [currentRootId]);

  useEffect(() => {
    if (!createWorktree || currentSession || mode === "command" || !currentRootId || !currentRootIsGitRepo) {
      setWorktreeBranchError("");
      return;
    }
    let active = true;
    setWorktreeBranchesLoading(true);
    setWorktreeBranchError("");
    fetchGitBranches(currentRootId)
      .then((payload) => {
        if (active) setWorktreeBranches(payload);
      })
      .catch((error) => {
        if (!active) return;
        setWorktreeBranches({ branches: [] });
        setWorktreeBranchError(error instanceof Error ? error.message : t("worktree.loadBranchFailed"));
      })
      .finally(() => {
        if (active) setWorktreeBranchesLoading(false);
      });
    return () => {
      active = false;
    };
  }, [createWorktree, currentRootId, currentRootIsGitRepo, currentSession, mode, t]);

  return {
    createWorktree,
    setCreateWorktree,
    worktreeBranchMode,
    setWorktreeBranchMode,
    worktreeBranch,
    setWorktreeBranch,
    worktreeBranches,
    worktreeBranchesLoading,
    worktreeBranchError,
  };
}
