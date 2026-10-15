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
  // related_worktree/worktree_missing 与后端列表 payload 同名（toSessionItem 透传）。
  aux_flags?: SessionAuxFlags;
  related_worktree?: RelatedWorktree | null;
  worktree_missing?: boolean;
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
  | { type: "session"; session: SessionItem }
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
