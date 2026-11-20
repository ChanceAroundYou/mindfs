import type React from "react";
import type { DirectorySortMode } from "../../services/prefs/directorySort";
import type { RelatedWorktree, SessionAuxFlags } from "../../shared/sessionTypes";

export type SessionType = "chat" | "plugin" | "command";

export type SessionItem = {
  key: string;
  session_key: string;
  root_id?: string;
  type?: SessionType;
  parent_session_key?: string;
  parent_tool_call_id?: string;
  source?: string;
  task_id?: string;
  /** 同一任务下的顶层会话数（后端列表查询下发）。分组靠它而不是靠当前这一页
   *  恰好装了几条 —— 多项目侧栏每根只取 6 条，任务成员很容易只落在窗口外。 */
  task_session_count?: number;
  agent?: string;
  shell?: string;
  name?: string;
  created_at?: string;
  updated_at?: string;
  pinned_at?: string | null;
  archived_at?: string | null;
  closed_at?: string;
  pending?: boolean;
  // 问号脉冲不另设字段：由 aux_flags.ask_user_waiting 驱动（见 SessionCard），
  // 后端从不下发 awaiting_user，留字段只会养出第二个真相。
  // aux_flags 是会话行的内容徽标数据源（G-AD，后端 session.SessionAuxFlags 同名）。
  // related_worktree/worktree_missing/worktree_active 与后端列表 payload 同名
  // （toSessionItem 透传）。绿色 worktree 标签只认 worktree_active。
  aux_flags?: SessionAuxFlags;
  related_worktree?: RelatedWorktree | null;
  worktree_missing?: boolean;
  worktree_active?: boolean;
  /** 派生四态（自己 pending / 等回答 / 后代在跑 / 全闲），由列表一次性盖上 */
  presence?: "running" | "awaiting" | "delegating" | "idle";
  related_files?: Array<{ path: string }>;
  search_seq?: number;
  search_snippet?: string;
  search_match_type?: "name" | "user" | "reply";
};

export type SessionListProps = {
  sessions: SessionItem[];
  selectedKey?: string;
  headerAction?: React.ReactNode;
  searchOpen?: boolean;
  searchResultsMode?: boolean;
  searchQuery?: string;
  searchLoading?: boolean;
  emptyText?: React.ReactNode;
  onSearchToggle?: () => void;
  onSearchBack?: () => void;
  onSearchQueryChange?: (query: string) => void;
  onSearchSubmit?: () => void;
  onSearchBlur?: () => void;
  syncingSessionKeys?: Set<string>;
  onSelect?: (session: SessionItem) => void;
  onSync?: (session: SessionItem) => Promise<void> | void;
  onPin?: (session: SessionItem, pinned: boolean) => Promise<boolean> | boolean;
  onRename?: (session: SessionItem, nextName: string) => Promise<boolean> | boolean;
  onDelete?: (session: SessionItem) => void;
  onArchive?: (session: SessionItem, archived: boolean) => Promise<boolean> | boolean;
  onLoadChildren?: (
    session: SessionItem,
    options?: { beforeTime?: string },
  ) => Promise<{ hasMore?: boolean } | void> | { hasMore?: boolean } | void;
  onLoadOlder?: () => void;
  loadingOlder?: boolean;
  hasMore?: boolean;
  // 归档入口行：只负责把「已归档对话」面板叫起来，内容在面板里，不在本列表。
  onOpenArchivePanel?: () => void;
};

export type VisibleSessionRow =
  // nested：这一行被收进了任务分组（见 sessionTaskGroups.ts）。只影响缩进，
  // 不改 isSubagent —— 分组里的会话仍然是普通会话，顶部徽标照常显示。
  // stripNamePrefix：成员名里与分组头重复的共享前缀，渲染时剥掉。
  | { type: "session"; session: SessionItem; nested?: boolean; stripNamePrefix?: string }
  // task-group：按 task_id 分组的容器行，成员紧随其后。默认折叠
  // （2026-11-20 用户决策：和子会话的展开一个逻辑），点这一行切换；
  // expanded 由 useSessionChildTree 决定 —— 选中的会话在组内时自动展开。
  | { type: "task-group"; taskId: string; label: string; number: string; expanded: boolean }
  | {
      type: "child-toggle";
      parent: SessionItem;
      loadedChildCount: number;
      hiddenCount: number;
      expanded: boolean;
    };

export type ProjectSessionGroup = {
  rootId: string;
  rootName: string;
  latestSessionTime?: string;
  sessions: SessionItem[];
  totalCount: number;
};

export type ProjectSessionListProps = {
  groups: ProjectSessionGroup[];
  selectedKey?: string;
  selectedRootId?: string;
  selectedNodeId?: string;
  headerAction?: React.ReactNode;
  loading?: boolean;
  emptyText?: React.ReactNode;
  projectSortMode?: DirectorySortMode;
  syncingSessionKeys?: Set<string>;
  onSearchToggle?: () => void;
  onSelect?: (session: SessionItem) => void;
  onSync?: (session: SessionItem) => Promise<void> | void;
  onPin?: (session: SessionItem, pinned: boolean) => Promise<boolean> | boolean;
  onRename?: (session: SessionItem, nextName: string) => Promise<boolean> | boolean;
  onDelete?: (session: SessionItem) => void;
  onArchive?: (session: SessionItem, archived: boolean) => Promise<boolean> | boolean;
  onLoadMoreProject?: (group: ProjectSessionGroup) => Promise<void> | void;
  onLoadChildren?: (
    session: SessionItem,
    options?: { beforeTime?: string },
  ) => Promise<{ hasMore?: boolean } | void> | { hasMore?: boolean } | void;
  // 归档入口行：点它弹出「已归档对话」视图。
  onOpenArchivePanel?: () => void;
  // 归档视图自带标题栏，就不要再画一遍空的操作栏（那排是搜索/导入入口，视图里用不上）。
  hideHeader?: boolean;
};

export type ForkSessionSource = {
  sessionKey: string;
  seq: number;
};
