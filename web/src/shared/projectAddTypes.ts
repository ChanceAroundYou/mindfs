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
  /** 正在新建文件夹（prompt → POST 之间），用于按钮防重。只有导入弹窗会置位。 */
  creating?: boolean;
  error: string;
};
