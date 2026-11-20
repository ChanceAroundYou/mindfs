/**
 * 杂项：更新态、插件视图上下文、根目录索引、响应式。
 * 拆自 appSupport.tsx（2026-09 App.tsx 拆分）。
 */

import { rootNodeKey } from "./appPath";
import { FILE_TOKEN_PATTERN } from "./appStorage";

import {  MessageKey ,  MessageParams  } from "../i18n";
import {  PluginInput  } from "../plugins/manager";
import {  FileEntry  } from "../services/prefs/directorySort";
import {  FilePayload  } from "../services/file";
import {  UpdateState  } from "../services/update";
import React, { useEffect, useState } from "react";

export const CHILD_SESSION_PAGE_SIZE = 100;

export const MULTI_PROJECT_SESSION_LIMIT = 6;

export const SESSION_PAGE_SIZE = 50;

export const APP_DOCUMENT_TITLE = "MindFS";

export type ManagedRootPayload = {
  id: string;
  display_name?: string;
  root_path?: string;
  is_git_repo?: boolean;
  is_git_worktree?: boolean;
  size?: number;
  mtime?: string;
};

export type LocalDirItemPayload = {
  name?: string;
  path?: string;
  is_dir?: boolean;
  is_added_root?: boolean;
  root_id?: string;
};

export type LocalDirsPayload = {
  path?: string;
  parent?: string;
  volumes?: LocalDirItemPayload[];
  items?: LocalDirItemPayload[];
};

export function managedDirAddErrorMessage(error: unknown, fallback: string, t: (key: MessageKey, params?: MessageParams) => string): string {
  const message = error instanceof Error ? error.message : String(error || "");
  if (message.includes("root name already exists")) {
    return t("root.nameAlreadyExists");
  }
  return message || fallback;
}

export function normalizeUpdateState(
  input: UpdateState | null | undefined,
): UpdateState {
  return {
    current_version: input?.current_version || "",
    latest_version: input?.latest_version || "",
    has_update: input?.has_update === true,
    status: input?.status || "idle",
    message: input?.message || "",
    release_name: input?.release_name || "",
    release_body: input?.release_body || "",
    release_url: input?.release_url || "",
    published_at: input?.published_at || "",
    last_checked_at: input?.last_checked_at || "",
    auto_update_supported: input?.auto_update_supported === true,
  };
}

export function updateButtonLabel(state: UpdateState, t: (key: MessageKey, params?: MessageParams) => string): string {
  const status = (state.status || "idle").toLowerCase();
  switch (status) {
    case "available":
      if (state.current_version && state.latest_version) {
        return t("update.available", { current: state.current_version, latest: state.latest_version });
      }
      return state.latest_version ? t("update.toVersion", { version: state.latest_version }) : t("update.newVersion");
    case "downloading":
      return t("update.downloading");
    case "installing":
      return t("update.installing");
    case "restarting":
      return t("update.restarting");
    case "failed":
      return t("update.failed");
    default:
      return t("update.latest");
  }
}

export function updateSummaryText(state: UpdateState, t: (key: MessageKey, params?: MessageParams) => string): string {
  const body = String(state.release_body || "").trim();
  if (body) {
    return body;
  }
  const name = String(state.release_name || "").trim();
  if (name) {
    return name;
  }
  if (state.latest_version) {
    return t("update.foundVersion", { version: state.latest_version });
  }
  return "";
}

export function shouldShowUpdateButton(state: UpdateState): boolean {
  const status = (state.status || "idle").toLowerCase();
  if (
    status === "downloading" ||
    status === "installing" ||
    status === "restarting" ||
    status === "failed"
  ) {
    return true;
  }
  return state.auto_update_supported === true && state.has_update === true;
}

export function waitForNextPaint(): Promise<void> {
  if (typeof window === "undefined" || typeof window.requestAnimationFrame !== "function") {
    return new Promise((resolve) => setTimeout(resolve, 0));
  }
  return new Promise((resolve) => {
    window.requestAnimationFrame(() => {
      window.requestAnimationFrame(() => resolve());
    });
  });
}

export function toPluginInput(
  file: FilePayload,
  query: Record<string, string>,
): PluginInput {
  return {
    name: file.name,
    path: file.path,
    content: file.content,
    ext: file.ext || "",
    mime: file.mime || "",
    size: typeof file.size === "number" ? file.size : 0,
    truncated: !!file.truncated,
    next_cursor:
      typeof file.next_cursor === "number" ? file.next_cursor : undefined,
    query,
  };
}

export function inferReadModeFromPlugin(plugin: any): "incremental" | "full" {
  if (!plugin) return "incremental";
  if (plugin?.fileLoadMode === "full") return "full";
  if (plugin?.fileLoadMode === "incremental") return "incremental";
  return "incremental";
}

export function buildMatchInputFromPath(
  path: string,
  query: Record<string, string>,
): PluginInput {
  const normalized = (path || "").replace(/\\/g, "/");
  const name = normalized.split("/").pop() || normalized;
  const dot = name.lastIndexOf(".");
  const ext = dot >= 0 ? name.slice(dot).toLowerCase() : "";
  return {
    name,
    path: normalized,
    content: "",
    ext,
    mime: "",
    size: 0,
    truncated: false,
    query,
  };
}

export function formatPluginViewContext(value: unknown): string {
  if (typeof value === "string") return value.trim();
  if (value == null) return "";
  try {
    return JSON.stringify(value, null, 2).trim();
  } catch {
    return String(value).trim();
  }
}

export function buildMessageWithViewContext(
  message: string,
  viewContext: unknown,
): string {
  const contextText = formatPluginViewContext(viewContext);
  if (!contextText) return message;
  return [contextText, "", message].join("\n");
}

export function mapManagedRootsToEntries(dirs: ManagedRootPayload[]): FileEntry[] {
  return dirs.map((dir) => {
    const d = dir as any;
    return {
      name: dir.display_name || dir.id.split("/").filter(Boolean).pop() || dir.id,
      path: dir.id,
      is_dir: true,
      is_root: true,
      size: typeof dir.size === "number" ? dir.size : undefined,
      mtime: typeof dir.mtime === "string" ? dir.mtime : undefined,
      // ponytail: 多节点着色由 FileTree 通过 rootEntries 扩展字段渲染
      _nodeId: (d as any)._nodeId as string | undefined,
      _nodeColor: d._nodeColor as string | undefined,
      _nodeName: d._nodeName as string | undefined,
    } as FileEntry & { _nodeId?: string; _nodeColor?: string; _nodeName?: string };
  });
}

export function indexManagedRoots(dirs: ManagedRootPayload[]): {
  byKey: Record<string, ManagedRootPayload>;
  byId: Record<string, ManagedRootPayload>;
  /**
   * rootId → 拥有它的节点 id（按注册表顺序、已去重）。
   *
   * `root_id` 只是一台机器内部的项目 id（常常就是目录名），同名项目在多台机器上各有一份
   * 是常态。要判断「裸 rootId 还够不够用」，必须先知道到底有几台机器拥有它。
   */
  nodesById: Record<string, string[]>;
} {
  const byKey: Record<string, ManagedRootPayload> = {};
  const nodesById: Record<string, string[]> = {};
  for (const dir of dirs || []) {
    const id = String(dir?.id || "");
    if (!id) continue;
    const nid = String((dir as any)._nodeId || "").trim();
    byKey[rootNodeKey(nid, id)] = dir;
    if (!nodesById[id]) nodesById[id] = [];
    if (!nodesById[id].includes(nid)) nodesById[id].push(nid);
  }
  // byId 仅作无节点上下文的回退，而且**只在唯一归属时**才填：同名项目保留首个正是
  // G-NP 之前所有错派的源头（fn 的 `docs` 卡被派给 pc，服务端回 `sql: no rows`）。
  // 多台机器都有同一个 id 时不猜 —— 查不到就说明「必须显式带节点」。
  // 当前选中的项目由 selectRootNode / refreshManagedRoots 单独注入这一格。
  const byId: Record<string, ManagedRootPayload> = {};
  for (const [id, owners] of Object.entries(nodesById)) {
    if (owners.length === 1) byId[id] = byKey[rootNodeKey(owners[0], id)];
  }
  return { byKey, byId, nodesById };
}

export function hasExplicitFileContext(message: string): boolean {
  return FILE_TOKEN_PATTERN.test(message);
}

/**
 * 按项目 id 清单取回**全部**节点上的项目条目。
 *
 * 不能用 `byId` 做这件事：`byId` 只在唯一归属时才有那一格（同名项目不猜），
 * 拿它重建根列表会让另一台机器上的同名项目从文件树里消失。`byKey` 才是全量。
 */
export function managedRootsForIds(
  ids: Iterable<string>,
  byKey: Record<string, ManagedRootPayload>,
): ManagedRootPayload[] {
  const wanted = new Set(Array.from(ids, (id) => String(id || "")).filter(Boolean));
  return Object.values(byKey || {}).filter((dir) => wanted.has(String((dir as any)?.id || "")));
}

export function useResponsive() {
  const [isMobile, setIsMobile] = useState(false);
  const [isTablet, setIsTablet] = useState(false);
  useEffect(() => {
    const checkSize = () => {
      const width = window.innerWidth;
      setIsMobile(width < 768);
      setIsTablet(width >= 768 && width < 1024);
    };
    checkSize();
    window.addEventListener("resize", checkSize);
    return () => window.removeEventListener("resize", checkSize);
  }, []);
  return { isMobile, isTablet };
}

export type AppProps = {
  onGoHome?: () => void;
};
