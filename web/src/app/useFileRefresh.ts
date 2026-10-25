/**
 * 目录树与当前文件内容刷新 —— A6b 簇（2 个单元，~110 行）。
 *
 * 覆盖：目录树刷新（refreshTreeDir）、当前文件内容刷新（refreshCurrentFileContent）。
 * 这两个是 App.tsx 里被引用最多的「叶子闭包」，单独成 hook 后其他簇可以直接
 * 消费，不再受它们晚声明的 TDZ 约束。
 *
 * 做法：hook，参数面 UseFileRefreshParams 即完整契约；函数体逐字搬入
 * （保持源码正则断言可命中，配合 source-map 注册）。
 */
import { useCallback, useMemo, type MutableRefObject, type Dispatch, type SetStateAction } from "react";
import { buildMatchInputFromPath, inferReadModeFromPlugin, useResponsive } from "./appMisc";
import { normalizeTreeResponse } from "./appUtils";
import { useActionHandlers } from "./useActionHandlers";
import { useNotificationSound } from "./useNotificationSound";
import { useExternalSessionImport } from "./useExternalSessionImport";
import { useGitActions } from "./useGitActions";
import { useGitData } from "./useGitData";
import { useManagedRoots } from "./useManagedRoots";
import { useMultiProjectSessions } from "./useMultiProjectSessions";
import { usePluginHandlers } from "./usePluginHandlers";
import { useProjectLifecycle } from "./useProjectLifecycle";
import { useProjectTreeWorktrees } from "./useProjectTreeWorktrees";
import { useSendMessage } from "./useSendMessage";
import { useSessionArchive } from "./useSessionArchive";
import { useSessionCache } from "./useSessionCache";
import { useSessionLifecycle } from "./useSessionLifecycle";
import { useSessionSearch } from "./useSessionSearch";
import { useSessionSidebarView } from "./useSessionSidebarView";
import { useSessionStreamCache } from "./useSessionStreamCache";
import { useTaskTemplates } from "./useTaskTemplates";
import { useI18n } from "../i18n";
import { PluginManager } from "../plugins/manager";
import { type FilePayload, fetchFile } from "../services/file";
import { protectedJSON as apiProtectedJSON } from "../services/net/api";
import { appURL } from "../services/net/base";
import { reportError } from "../services/net/error";
import { type FileEntry } from "../services/prefs/directorySort";

export type UseFileRefreshParams = {
  currentRootIdRef: MutableRefObject<string | null>;
  file: FilePayload | null;
  fileCursorRef: MutableRefObject<number>;
  fileEditStore: any;
  fileRef: MutableRefObject<FilePayload | null>;
  formatDirectoryLoadError: (message?: string | null) => any;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  invalidTreeCacheKeysRef: MutableRefObject<Set<string>>;
  pluginBypassRef: MutableRefObject<boolean>;
  pluginManagerRef: MutableRefObject<PluginManager>;
  pluginQueryRef: MutableRefObject<Record<string, string>>;
  selectedDirRef: MutableRefObject<string | null>;
  setEntriesByPath: Dispatch<SetStateAction<Record<string, FileEntry[]>>>;
  setFile: Dispatch<SetStateAction<FilePayload | null>>;
  setMainDirectoryError: Dispatch<SetStateAction<string>>;
  setMainEntries: Dispatch<SetStateAction<FileEntry[]>>;
  treeCacheKey: (rootID: string, dirPath: string) => any;
  treeFetchSeqRef: MutableRefObject<Record<string, number>>;
};

export function useFileRefresh(params: UseFileRefreshParams) {
  const {
    currentRootIdRef,
    file,
    fileCursorRef,
    fileEditStore,
    fileRef,
    formatDirectoryLoadError,
    getNodeIdForRoot,
    invalidTreeCacheKeysRef,
    pluginBypassRef,
    pluginManagerRef,
    pluginQueryRef,
    selectedDirRef,
    setEntriesByPath,
    setFile,
    setMainDirectoryError,
    setMainEntries,
    treeCacheKey,
    treeFetchSeqRef,
  } = params;

  const refreshTreeDir = useCallback(
    async (rootID: string, dirPath: string, syncMain: boolean) => {
      const cacheKey = treeCacheKey(rootID, dirPath);
      // 并发守卫：同目录并发刷新（按钮 × WS 事件 × 导航）时只让最后发起的请求生效，
      // 防止先发起的旧响应后返回覆盖新数据（“点了刷新仍显示旧内容”的来源之一）。
      treeFetchSeqRef.current[cacheKey] = (treeFetchSeqRef.current[cacheKey] || 0) + 1;
      const seq = treeFetchSeqRef.current[cacheKey];
      // syncMain 时须仍在查看该目录才允许回写主视图，防止导航后旧响应覆盖新视图
      const matchesView = () =>
        rootID === currentRootIdRef.current &&
        (selectedDirRef.current === rootID ? "." : selectedDirRef.current || ".") === dirPath;
      try {
        const payload = await apiProtectedJSON<any>(
          appURL("/api/tree", new URLSearchParams({ root: rootID, dir: dirPath }), getNodeIdForRoot(rootID)),
        );
        if (treeFetchSeqRef.current[cacheKey] !== seq) {
          return; // 旧响应已过时，丢弃
        }
        const parsed = normalizeTreeResponse(payload);
        invalidTreeCacheKeysRef.current.delete(cacheKey);
        setEntriesByPath((prev) => ({
          ...prev,
          [cacheKey]: parsed.entries,
        }));
        if (syncMain && matchesView()) {
          setMainDirectoryError("");
          setMainEntries(parsed.entries);
        }
      } catch (error) {
        if (treeFetchSeqRef.current[cacheKey] !== seq) {
          return; // 已被更新的请求取代，错误也一并丢弃
        }
        // 失败后不信任旧缓存：标记该目录缓存失效，下次查看/刷新强制重拉真实状态
        invalidTreeCacheKeysRef.current.add(cacheKey);
        if (syncMain && matchesView()) {
          const message = String(
            (error as any)?.payload?.error || (error as any)?.payload?.message || (error as Error)?.message || "",
          );
          const display = formatDirectoryLoadError(message);
          setMainDirectoryError(display);
          reportError("file.read_failed", display, { severity: "warning", recoverable: true });
        }
      }
    },
    [formatDirectoryLoadError, getNodeIdForRoot, treeCacheKey],
  );
  const refreshCurrentFileContent = useCallback(
    async (rootID: string, changedPath: string) => {
      if (fileEditStore.has(rootID, changedPath)) return;
      const currentFile = fileRef.current;
      if (!currentFile) return;
      const currentRoot = currentFile.root || currentRootIdRef.current || "";
      if (currentRoot !== rootID || currentFile.path !== changedPath) return;

      let readMode: "incremental" | "full" = "incremental";
      if (!pluginBypassRef.current) {
        try {
          const plugin = pluginManagerRef.current.match(
            rootID,
            buildMatchInputFromPath(changedPath, pluginQueryRef.current),
          );
          readMode = inferReadModeFromPlugin(plugin);
        } catch {
          readMode = "incremental";
        }
      }

      try {
        const next = await fetchFile({
          rootId: rootID,
          path: changedPath,
          nodeId: getNodeIdForRoot(String(rootID)),
          readMode,
          cursor: fileCursorRef.current || 0,
        });
        const latestFile = fileRef.current;
        const latestRoot = latestFile?.root || currentRootIdRef.current || "";
        if (
          !next ||
          !latestFile ||
          latestRoot !== rootID ||
          latestFile.path !== changedPath ||
          fileEditStore.has(rootID, changedPath)
        ) {
          return;
        }
        setFile({
          ...next,
          targetLine: latestFile.targetLine,
          targetColumn: latestFile.targetColumn,
        });
      } catch (err) {
        console.error("[file.refresh.changed] failed", {
          rootID,
          changedPath,
          err,
        });
      }
    },
    [],
  );

  return {
    refreshTreeDir,
    refreshCurrentFileContent,
  };
}
