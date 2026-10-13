/** State shared by the project-add view and app-level project/realtime logic. */
export type ProjectAddMode =
  | "mode"
  | "local"
  | "blank_location"
  | "github_location"
  | "github"
  | "worktree_location";

export type GitHubImportState = {
  url: string;
  parentPath: string;
  taskId: string;
  status: string;
  message: string;
  running: boolean;
  submitting: boolean;
  done: boolean;
  error: string;
};
