import type { ReactNode } from "react";
import type { DirectorySortMode, FileEntry } from "../../shared/directorySort";
import type { SendShortcut } from "../../shared/sendShortcut";
import type { FontSizePreferences } from "../../shared/fontSize";

// FileTree 的 props 契约与共享类型。单独成文件（2026-10-08）是为了让 App.tsx、
// FileTreePanel.tsx、app/useProjectTree* 直接引用，不必经由 FileTree.tsx 这个
// 组件模块中转 —— 那些引用全是 `import type`，运行期被擦除，不产生环。

export type FileMeta = {
  source_session?: string;
  session_name?: string;
};


export type RootSessionIndicator = {
  bound?: boolean;
  pending?: boolean;
};

export type ProjectTreeTab = "files" | "git" | "worktrees" | "related";
export type AgentConfigSwitchRequest = {
  nonce: number;
  providerIDs?: string[];
};

export const PROJECT_TREE_ROOT_PADDING_LEFT = 0;
export const PROJECT_TREE_INDENT = 16;

export type FileTreeProps = {
  entries: FileEntry[];
  childrenByPath: Record<string, FileEntry[]>;
  expanded: string[];
  sortMode: DirectorySortMode;
  showHiddenFiles?: boolean;
  selectedDirKey?: string | null;
  selectedPath?: string | null;
  rootId?: string | null;
  rootNodeId?: string | null;
  rootColor?: string | null;
  rootSessionIndicators?: Record<string, RootSessionIndicator>;
  fileMetas?: Record<string, FileMeta>;
  activeSessionKey?: string | null;
  onSortModeChange?: (mode: DirectorySortMode) => void;
  onShowHiddenFilesChange?: (show: boolean) => void;
  onRefresh?: (tab: ProjectTreeTab) => void | Promise<void>;
  onNodeManagerRefresh?: () => Promise<void>;
  onSelectFile?: (entry: FileEntry, rootId: string) => void;
  onSelectRoot?: (entry: FileEntry, rootId: string) => void;
  /** 点文件夹「名字」= 打开该目录（由 App 决定是否切主面板）；与 onToggleDir 的纯展开收起区分。 */
  onSelectDir?: (entry: FileEntry, rootId: string) => void;
  onToggleDir?: (entry: FileEntry, rootId: string) => void;
  renderRootExtraContent?: (rootId: string) => ReactNode;
  renderRootWorktreeContent?: (rootId: string) => ReactNode;
  renderRootRelatedContent?: (rootId: string) => ReactNode;
  agentConfigSwitchRequest?: AgentConfigSwitchRequest | null;
  onAgentConfigSwitched?: (agent: string) => void;
  /**
   * 项目树当前页签。**受控**：唯一真相在 App 层（useAppFileWorkspaceState，默认
   * "files"）。原先这里自己读 localStorage 初始化，而那个键**从来没人写** ——
   * 于是侧边栏永远停在一个陈旧的 "git" 上（用户实测「打开文件树就跳到 git 页」）。
   */
  projectTreeTab?: ProjectTreeTab;
  onProjectTreeTabChange?: (tab: ProjectTreeTab) => void;
  creatingRootName?: string | null;
  creatingRootBusy?: boolean;
  creatingRootExtraContent?: ReactNode;
  creatingRootSubmitOnBlur?: boolean;
  onCreateRootStart?: () => void;
  onOpenProjectAdd?: () => void;
  onCreateRootNameChange?: (name: string) => void;
  onCreateRootSubmit?: () => void;
  onCreateRootCancel?: () => void;
  projectAddOverlay?: ReactNode;
  updateActionLabel?: string | null;
  updateActionDisabled?: boolean;
  updateActionHelp?: string | null;
  updateActionBusy?: boolean;
  updateActionSummary?: string | null;
  onUpdateAction?: () => void;
  showEnterKeySendOption?: boolean;
  enterKeySends?: boolean;
  onEnterKeySendsChange?: (enabled: boolean) => void;
  showSendShortcutOption?: boolean;
  sendShortcut?: SendShortcut | null;
  onSendShortcutChange?: (shortcut: SendShortcut | null) => void;
  sidebarsSwapped?: boolean;
  onSidebarsSwappedChange?: (enabled: boolean) => void;
  gitDiffSideBySide?: boolean;
  onGitDiffSideBySideChange?: (enabled: boolean) => void;
  multiProjectSessionsEnabled?: boolean;
  onMultiProjectSessionsChange?: (enabled: boolean) => void;
  fontSizePreferences?: FontSizePreferences;
  onFontSizePreferencesChange?: (preferences: FontSizePreferences) => void;
  onRunAgentLifecycleCommand?: (agentName: string, action: AgentLifecycleCommandAction, commands: string[]) => void | Promise<void>;
  onRestartAgent?: (agentName: string) => void | Promise<void>;
  onGoHome?: () => void;
  footerTopContent?: ReactNode;
};

export type AgentConfigSwitchSelection = { type: "backup" | "api_provider"; id: string };
export type AgentLifecycleCommandAction = "install" | "update";
