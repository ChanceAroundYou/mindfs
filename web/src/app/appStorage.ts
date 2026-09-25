/**
 * 应用内持久化：localStorage 键与读写助手。
 * 所有键都经 accountScopedKey 做账户分区——多配置档之间不能串号。
 * 拆自 appSupport.tsx（2026-09 App.tsx 拆分）。
 */

import { type MainViewMode } from "./appPath";
import { MAIN_VIEW_MODES } from "./appPath";

import {  currentUser  } from "../services/authGate";
import {  getRootNodeId  } from "../services/rootNode";
import {  scopeKey, dirSelKey  } from "../services/scope";

export const PLUGIN_QUERY_STORAGE_PREFIX = "vp-progress:";

export const TREE_SORT_STORAGE_KEY = "mindfs-tree-sort-mode";

export const DIRECTORY_SORT_OVERRIDES_STORAGE_KEY = "mindfs-directory-sort-overrides";

export const FILE_SCROLL_STORAGE_KEY = "mindfs-file-scroll-positions";

export const LAST_ROOT_STORAGE_KEY = "mindfs-last-root-id";

export const LAST_ROOT_NODE_STORAGE_KEY = "mindfs-last-root-node";

export function accountScopedKey(base: string): string {
  const name = String(currentUser()?.username || "").trim();
  return name ? `${base}::${name}` : base;
}

export const GIT_STATUS_EXPANDED_STORAGE_KEY = "mindfs-git-status-expanded";

export const GIT_HISTORY_EXPANDED_STORAGE_KEY = "mindfs-git-history-expanded";

export const TASK_TEMPLATE_SELECTION_STORAGE_KEY = "mindfs-task-template-selection";

export const TASK_TEMPLATE_ALL_FILTER = "__all__";

export const CANDIDATE_FETCH_DEBOUNCE_MS = 512;

export const FILE_TOKEN_PATTERN = /\[(?:read file|file):\s*[^\]]+\]/i;

export function loadPersistedFileScrollPositions(): Record<string, number> {
  if (typeof window === "undefined") {
    return {};
  }
  try {
    const raw = window.localStorage.getItem(FILE_SCROLL_STORAGE_KEY);
    if (!raw) {
      return {};
    }
    const parsed = JSON.parse(raw) as Record<string, unknown>;
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      return {};
    }
    const next: Record<string, number> = {};
    Object.entries(parsed).forEach(([key, value]) => {
      const scrollTop = Number(value);
      if (!key || !Number.isFinite(scrollTop) || scrollTop < 0) {
        return;
      }
      next[key] = scrollTop;
    });
    return next;
  } catch {
    return {};
  }
}

export function persistFileScrollPositions(positions: Record<string, number>): void {
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.localStorage.setItem(
      FILE_SCROLL_STORAGE_KEY,
      JSON.stringify(positions),
    );
  } catch {}
}

export function pluginQueryStorageKey(
  root: string,
  file: string,
  nodeId?: string,
): string {
  // 多节点同名项目按节点隔离；空 nodeId 与历史格式逐字节一致
  return `${PLUGIN_QUERY_STORAGE_PREFIX}${scopeKey(nodeId, root)}:${file}`;
}

export function loadPersistedPluginQuery(
  root: string,
  file: string,
  nodeId?: string,
): Record<string, string> {
  if (!root || !file) return {};
  try {
    const raw =
      window.localStorage.getItem(pluginQueryStorageKey(root, file, nodeId)) ||
      // 旧版本存的是裸键：scoped 键 miss 时回退，保证历史数据仍能读到
      window.localStorage.getItem(pluginQueryStorageKey(root, file));
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed))
      return {};
    const next: Record<string, string> = {};
    Object.entries(parsed as Record<string, unknown>).forEach(
      ([key, value]) => {
        if (!key) return;
        next[key] = String(value);
      },
    );
    return next;
  } catch {
    return {};
  }
}

export function persistPluginQuery(
  root: string,
  file: string,
  query: Record<string, string>,
  nodeId?: string,
): void {
  if (!root || !file) return;
  try {
    window.localStorage.setItem(
      pluginQueryStorageKey(root, file, nodeId),
      JSON.stringify(query || {}),
    );
  } catch {}
}

export function removeLocalStorageByPrefix(prefix: string): void {
  if (typeof window === "undefined" || !prefix) {
    return;
  }
  try {
    for (const key of Array.from(
      { length: window.localStorage.length },
      (_, index) => window.localStorage.key(index),
    ).filter(Boolean) as string[]) {
      if (key.startsWith(prefix)) {
        window.localStorage.removeItem(key);
      }
    }
  } catch {}
}

export function loadLastRootId(): string {
  if (typeof window === "undefined") {
    return "";
  }
  return window.localStorage.getItem(accountScopedKey(LAST_ROOT_STORAGE_KEY)) || "";
}

export function loadLastRootNodeId(): string {
  if (typeof window === "undefined") {
    return "";
  }
  return window.localStorage.getItem(accountScopedKey(LAST_ROOT_NODE_STORAGE_KEY)) || "";
}

export function loadBooleanRecord(key: string): Record<string, boolean> {
  if (typeof window === "undefined") {
    return {};
  }
  try {
    const parsed = JSON.parse(window.localStorage.getItem(key) || "{}") as Record<string, unknown>;
    return Object.fromEntries(
      Object.entries(parsed).filter(([, value]) => typeof value === "boolean"),
    ) as Record<string, boolean>;
  } catch {
    return {};
  }
}

export function loadStringBooleanRecord(key: string): Record<string, Record<string, boolean>> {
  if (typeof window === "undefined") {
    return {};
  }
  try {
    const parsed = JSON.parse(window.localStorage.getItem(key) || "{}") as Record<string, unknown>;
    return Object.fromEntries(
      Object.entries(parsed).map(([root, value]) => [
        root,
        value && typeof value === "object"
          ? Object.fromEntries(
              Object.entries(value as Record<string, unknown>).filter(([, expanded]) => typeof expanded === "boolean"),
            )
          : {},
      ]),
    ) as Record<string, Record<string, boolean>>;
  } catch {
    return {};
  }
}

export const MOBILE_ENTER_KEY_SEND_STORAGE_KEY = "mindfs-mobile-enter-key-sends";

export const SIDEBARS_SWAPPED_STORAGE_KEY = "mindfs-sidebars-swapped";

export const GIT_DIFF_SIDE_BY_SIDE_STORAGE_KEY = "mindfs-git-diff-side-by-side";

export const TASK_CREATE_WORKTREE_PREF_STORAGE_KEY = "mindfs-task-create-worktree-pref";

export type TaskCreateWorktreePreference = {
  createWorktree: boolean;
  worktreeBranchMode: "new" | "existing";
  worktreeBranch: string;
};

export const MAIN_VIEW_STORAGE_KEY = "mindfs-main-view";

// 跨项目工作台：筛选档 + 折叠的项目组（键是 scopeKey(nodeId, rootId) 列表）。
// 项目数一多就该靠「收窄」而不是「加层级」来扫读，所以这两项都要跨会话记住。
export const WORKSPACE_FILTER_STORAGE_KEY = "mindfs-workspace-filter";
export const WORKSPACE_COLLAPSED_STORAGE_KEY = "mindfs-workspace-collapsed";

export const WORKSPACE_FILTERS = ["all", "active", "blocked"] as const;
export type WorkspaceBoardFilter = (typeof WORKSPACE_FILTERS)[number];

export function loadWorkspaceFilter(): WorkspaceBoardFilter {
  if (typeof window === "undefined") return "all";
  try {
    const saved = window.localStorage.getItem(WORKSPACE_FILTER_STORAGE_KEY);
    return WORKSPACE_FILTERS.includes(saved as WorkspaceBoardFilter) ? (saved as WorkspaceBoardFilter) : "all";
  } catch {
    return "all";
  }
}

export function saveWorkspaceFilter(filter: WorkspaceBoardFilter): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(WORKSPACE_FILTER_STORAGE_KEY, filter);
  } catch {}
}

export function loadWorkspaceCollapsed(): Set<string> {
  if (typeof window === "undefined") return new Set<string>();
  try {
    const raw = window.localStorage.getItem(WORKSPACE_COLLAPSED_STORAGE_KEY);
    if (!raw) return new Set<string>();
    const parsed = JSON.parse(raw);
    return new Set(Array.isArray(parsed) ? parsed.filter((v): v is string => typeof v === "string") : []);
  } catch {
    return new Set<string>();
  }
}

export function saveWorkspaceCollapsed(keys: Set<string>): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(WORKSPACE_COLLAPSED_STORAGE_KEY, JSON.stringify(Array.from(keys)));
  } catch {}
}

export function loadMainView(): MainViewMode {
  if (typeof window === "undefined") return "board";
  try {
    const saved = window.localStorage.getItem(MAIN_VIEW_STORAGE_KEY);
    return MAIN_VIEW_MODES.includes(saved as MainViewMode) ? (saved as MainViewMode) : "board";
  } catch {
    return "board";
  }
}

export function loadLegacyMainView(): MainViewMode | null {
  if (typeof window === "undefined") return null;
  if (window.localStorage.getItem(MAIN_VIEW_STORAGE_KEY)) return null;
  try {
    const legacy = window.localStorage.getItem("mindfs-default-main-content-view");
    const migrated: MainViewMode = legacy === "file-browser" ? "files" : "board";
    window.localStorage.setItem(MAIN_VIEW_STORAGE_KEY, migrated);
    return migrated;
  } catch {
    return null;
  }
}

export function loadMobileEnterKeySends(): boolean {
  if (typeof window === "undefined") {
    return false;
  }
  try {
    return window.localStorage.getItem(MOBILE_ENTER_KEY_SEND_STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}

export function loadSidebarsSwapped(): boolean {
  if (typeof window === "undefined") {
    return false;
  }
  try {
    return window.localStorage.getItem(SIDEBARS_SWAPPED_STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}

export function loadGitDiffSideBySide(): boolean {
  if (typeof window === "undefined") {
    return false;
  }
  try {
    return window.localStorage.getItem(GIT_DIFF_SIDE_BY_SIDE_STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}

export function loadTaskCreateWorktreePreference(rootId: string): TaskCreateWorktreePreference {
  if (typeof window === "undefined" || !rootId) {
    return { createWorktree: false, worktreeBranchMode: "new", worktreeBranch: "" };
  }
  try {
    const parsed = JSON.parse(window.localStorage.getItem(TASK_CREATE_WORKTREE_PREF_STORAGE_KEY) || "{}") as Record<string, unknown>;
    const value = parsed[scopeKey(getRootNodeId(rootId) ?? "", rootId)] as Record<string, unknown> | undefined;
    return {
      createWorktree: value?.createWorktree === true,
      worktreeBranchMode: value?.worktreeBranchMode === "existing" ? "existing" : "new",
      worktreeBranch: typeof value?.worktreeBranch === "string" ? value.worktreeBranch : "",
    };
  } catch {
    return { createWorktree: false, worktreeBranchMode: "new", worktreeBranch: "" };
  }
}

export function saveTaskCreateWorktreePreference(rootId: string, pref: TaskCreateWorktreePreference): void {
  if (typeof window === "undefined" || !rootId) return;
  try {
    const parsed = JSON.parse(window.localStorage.getItem(TASK_CREATE_WORKTREE_PREF_STORAGE_KEY) || "{}") as Record<string, unknown>;
    window.localStorage.setItem(TASK_CREATE_WORKTREE_PREF_STORAGE_KEY, JSON.stringify({
      ...parsed,
      [scopeKey(getRootNodeId(rootId) ?? "", rootId)]: pref,
    }));
  } catch {
    // Ignore storage failures; the current dialog state can still be used.
  }
}
