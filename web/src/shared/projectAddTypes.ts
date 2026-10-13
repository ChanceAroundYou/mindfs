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

export type LocalDirItem = {
  name: string;
  path: string;
  is_dir: boolean;
  is_added_root?: boolean;
  root_id?: string;
};

export type LocalDirBrowserState = {
  path: string;
  parent?: string;
  volumes?: LocalDirItem[];
  items: LocalDirItem[];
  loading: boolean;
  selectedPath: string;
  adding: boolean;
  error: string;
};
