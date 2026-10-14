import type { CachedGitDiffPayload } from "../file";
import type { GitStatusCode } from "../../shared/gitTypes";
export type {
  GitActionPayload,
  GitBranchItem,
  GitBranchesPayload,
  GitCommitFilesPayload,
  GitHistoryItem,
  GitHistoryPayload,
  GitStatusCode,
  GitStatusItem,
  GitStatusPayload,
  GitWorktreeItem,
  GitWorktreesPayload,
} from "../../shared/gitTypes";

export type GitDiffPayload = CachedGitDiffPayload & {
  path: string;
  display_path?: string;
  old_path?: string;
  status: GitStatusCode | string;
  additions: number;
  deletions: number;
  content: string;
  commit?: string;
  base_head?: string;
  target_head?: string;
  source?: "worktree" | "commit" | "commit_range";
};
