/**
 * 路径规范化与 URL 状态。
 * URLState 是主面板路由的唯一真源，改动会影响深链。
 * 拆自 appSupport.tsx（2026-09 App.tsx 拆分）。
 */

import {  DirectorySortMode  } from "../services/directorySort";
import {  scopeKey, dirSelKey  } from "../services/scope";

export type URLState = {
  root: string;
  node?: string;
  file: string;
  session: string;
  cursor: number;
  pluginQuery: Record<string, string>;
  // 主面板模式也进 URL：按钮高亮与面板显示必须同源恢复（缺省时回落 localStorage）。
  view?: MainViewMode;
};

export function buildFileScrollKey(
  rootId: string | null | undefined,
  path: string | null | undefined,
): string {
  if (!rootId || !path) {
    return "";
  }
  return `${rootId}::${path}`;
}

export function parsePluginQuery(search: string): Record<string, string> {
  const params = new URLSearchParams(search);
  const query: Record<string, string> = {};
  params.forEach((value, key) => {
    if (key.startsWith("vp_")) {
      query[key.slice("vp_".length)] = value;
    }
  });
  return query;
}

export function parseCursor(value: string | null): number {
  if (!value) return 0;
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed) || parsed < 0) return 0;
  return parsed;
}

export function normalizeCursor(value: unknown): number | null {
  if (value === undefined || value === null || value === "") {
    return null;
  }
  const parsed = Number.parseInt(String(value), 10);
  if (!Number.isFinite(parsed) || parsed < 0) {
    return 0;
  }
  return parsed;
}

export function isDirectorySortMode(
  value: string | null | undefined,
): value is DirectorySortMode {
  return (
    value === "name-asc" ||
    value === "name-desc" ||
    value === "mtime-desc" ||
    value === "mtime-asc" ||
    value === "size-desc" ||
    value === "size-asc"
  );
}

export function readURLState(): URLState {
  const params = new URLSearchParams(window.location.search);
  const view = params.get("view");
  return {
    root: params.get("root") || "",
    node: params.get("node") || "",
    file: params.get("file") || "",
    session: params.get("session") || "",
    cursor: parseCursor(params.get("cursor")),
    pluginQuery: parsePluginQuery(window.location.search),
    view: MAIN_VIEW_MODES.includes(view as MainViewMode)
      ? (view as MainViewMode)
      : undefined,
  };
}

export function buildURLSearch(next: URLState): string {
  const params = new URLSearchParams();
  if (next.root) params.set("root", next.root);
  if (next.node) params.set("node", next.node);
  if (next.file) params.set("file", next.file);
  if (next.session) params.set("session", next.session);
  if (next.cursor > 0) params.set("cursor", String(next.cursor));
  if (next.view) params.set("view", next.view);
  Object.entries(next.pluginQuery).forEach(([key, value]) => {
    if (!key) return;
    params.set(`vp_${key}`, String(value));
  });
  const encoded = params.toString();
  return encoded ? `?${encoded}` : "";
}

export function normalizePath(value: string): string {
  return String(value || "")
    .replace(/\\/g, "/")
    .replace(/^\/+|\/+$/g, "");
}

export function relativeDisplayPathFromRoot(rootPath: string | undefined, absolutePath: string): string {
  const root = normalizePath(rootPath || "");
  const target = normalizePath(absolutePath);
  if (!target) return "";
  if (!root) return target;
  if (target === root) return ".";
  if (target.startsWith(`${root}/`)) {
    return target.slice(root.length + 1);
  }
  const rootParts = root.split("/").filter(Boolean);
  const targetParts = target.split("/").filter(Boolean);
  let shared = 0;
  while (
    shared < rootParts.length &&
    shared < targetParts.length &&
    rootParts[shared] === targetParts[shared]
  ) {
    shared += 1;
  }
  const upward = Array(Math.max(0, rootParts.length - shared)).fill("..");
  const downward = targetParts.slice(shared);
  return [...upward, ...downward].join("/") || ".";
}

export function joinDisplayPath(base: string, path: string): string {
  const normalizedBase = normalizePath(base);
  const normalizedPath = normalizePath(path);
  if (!normalizedBase) return normalizedPath;
  if (!normalizedPath) return normalizedBase;
  return `${normalizedBase}/${normalizedPath}`;
}

export function parseFileLocation(path: string): {
  path: string;
  targetLine?: number;
  targetColumn?: number;
} {
  const raw = String(path || "");
  const [base, fragment = ""] = raw.split("#", 2);
  if (fragment) {
    const match = /^L(\d+)(?:C(\d+))?$/i.exec(fragment.trim());
    if (match) {
      const targetLine = Number.parseInt(match[1], 10);
      const targetColumn = match[2] ? Number.parseInt(match[2], 10) : undefined;
      return {
        path: base,
        targetLine:
          Number.isFinite(targetLine) && targetLine > 0 ? targetLine : undefined,
        targetColumn:
          targetColumn && Number.isFinite(targetColumn) && targetColumn > 0
            ? targetColumn
            : undefined,
      };
    }
  }

  const colonMatch = /^(.*):(\d+)(?::(\d+))?$/.exec(base.trim());
  if (!colonMatch) {
    return { path: base };
  }
  const targetLine = Number.parseInt(colonMatch[2], 10);
  const targetColumn = colonMatch[3]
    ? Number.parseInt(colonMatch[3], 10)
    : undefined;
  return {
    path: colonMatch[1],
    targetLine:
      Number.isFinite(targetLine) && targetLine > 0 ? targetLine : undefined,
    targetColumn:
      targetColumn && Number.isFinite(targetColumn) && targetColumn > 0
        ? targetColumn
        : undefined,
  };
}

export function parentDirsOfFile(path: string): string[] {
  const normalized = normalizePath(path);
  if (!normalized) return [];
  const parts = normalized.split("/").filter(Boolean);
  if (parts.length <= 1) return [];
  const dirs: string[] = [];
  for (let i = 1; i < parts.length; i++) {
    dirs.push(parts.slice(0, i).join("/"));
  }
  return dirs;
}

export function dirnameOfPath(path: string): string {
  const normalized = normalizePath(path);
  if (!normalized) return ".";
  const parts = normalized.split("/").filter(Boolean);
  if (parts.length <= 1) return ".";
  return parts.slice(0, -1).join("/");
}

export function basenameOfPath(path: string): string {
  const normalized = normalizePath(path);
  if (!normalized) return "";
  const parts = normalized.split("/").filter(Boolean);
  return parts[parts.length - 1] || normalized;
}

export function comparableManagedRootPath(value: string | undefined): string {
  return String(value || "").trim().replace(/\\/g, "/").replace(/\/+$/, "");
}

export function buildDirectorySelectionKey(
  nodeId: string | null | undefined,
  root: string,
  path: string,
  isRoot: boolean,
): string {
  return dirSelKey(nodeId, root, path, isRoot);
}

export function rootNodeKey(nodeId: string | null | undefined, rootId: string): string {
  return scopeKey(nodeId, rootId);
}

export type MainViewMode = "workspace" | "board" | "files" | "chat";

export const MAIN_VIEW_MODES: MainViewMode[] = ["workspace", "board", "files", "chat"];
