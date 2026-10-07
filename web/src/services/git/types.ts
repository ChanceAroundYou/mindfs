import type { CachedGitDiffPayload } from "../file";

export type GitStatusCode = "M" | "A" | "D" | "R" | "??";

export type GitStatusItem = {
  path: string;
  display_path?: string;
  old_path?: string;
  status: GitStatusCode;
  staged?: boolean;
  additions: number;
  deletions: number;
  is_dir?: boolean;
};

export type GitStatusPayload = {
  available: boolean;
  branch?: string;
  dirty_count: number;
  items: GitStatusItem[];
};

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

export type GitHistoryItem = {
  hash: string;
  message: string;
  commit_time: string;
  remote?: boolean;
};

export type GitHistoryPayload = {
  available: boolean;
  items: GitHistoryItem[];
  has_more: boolean;
  commit_missing?: boolean;
  remote_head?: string;
};

export type GitCommitFilesPayload = {
  commit: string;
  items: GitStatusItem[];
};

export type GitBranchItem = {
  name: string;
  current: boolean;
};

export type GitBranchesPayload = {
  current?: string;
  branches: GitBranchItem[];
};

export type GitWorktreeItem = {
  path: string;
  branch?: string;
  head?: string;
  current: boolean;
};

export type GitWorktreesPayload = {
  items: GitWorktreeItem[];
};

export type GitActionPayload = {
  output: string;
  status: GitStatusPayload;
};
