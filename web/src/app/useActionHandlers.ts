/**
 * 文件/目录动作处理器 —— App.tsx 里最大的 useMemo（~430 行）。
 *
 * 为什么单独抽：它闭包了 44 个 ref/setter/局部函数/状态，是 App.tsx 状态图里
 * 最密的一团（比 handleSendMessage 还多）。留在 App.tsx 里，任何一处改动都要在
 * 1 万行里翻找这 44 个名字；抽出来后，参数面（ActionHandlersParams）就是它的
 * 完整契约。
 *
 * 做法：hook 而非模块函数 —— 函数体使用 `t()`（useI18n）和 `isMobile`
 * （useResponsive），都是 hook。所以做成 useActionHandlers()，内部取这两个值，
 * 再把函数体逐字搬进来。
 *
 * 自引用：handlers 内部通过 actionHandlersRef.current.open_dir(...) 回调自身。
 * 用 MutableRefObject<ActionHandlers> 传进 hook，App.tsx 里用 useEffect 同步。
 */

import {
  useMemo,
  type MutableRefObject,
  type RefObject,
  type Dispatch,
  type SetStateAction,
} from "react";
import type { FileEntry } from "../services/prefs/directorySort";
import type { FilePayload } from "../services/file";
import type { GitDiffPayload } from "../services/git";
import type { MainViewMode, URLState } from "../app/appPath";
import type { ManagedRootPayload } from "../app/appMisc";
import type { PluginManager } from "../plugins/manager";
import {
  buildMatchInputFromPath,
  inferReadModeFromPlugin,
  useResponsive,
} from "../app/appMisc";
import {
  buildDirectorySelectionKey,
  dirnameOfPath,
  normalizeCursor,
  parentDirsOfFile,
  parseFileLocation,
  parsePluginQuery,
} from "../app/appPath";
import {
  loadPersistedPluginQuery,
  persistPluginQuery,
} from "../app/appStorage";
import { fetchFile, getCachedFile } from "../services/file";
import { FileEditStore, fileEditKey } from "../services/fileEditing";
import { normalizePathForRoot } from "../services/fileNavigation";
import { normalizeTreeResponse } from "../app/appUtils";
import { reportError } from "../services/net/error";
import { ProtectedAPIError, protectedJSON as apiProtectedJSON } from "../services/net/api";
import { appURL } from "../services/net/base";
import { scopeKey, expandKey, dirSelKey } from "../shared/scope";
import { useI18n } from "../i18n";

/** handlers 对象的形状（open / open_dir 两个动作）。 */
export type ActionHandlers = {
  open: (params: any) => Promise<void>;
  open_dir: (params: any) => Promise<void>;
};

/** useActionHandlers 的完整参数面：44 个闭包。 */
export type ActionHandlersParams = {
  actionHandlersRef: MutableRefObject<ActionHandlers>;
  fileEditStore: FileEditStore;
  currentRootIdRef: RefObject<string | null>;
  currentRootNodeIdRef: RefObject<string | null>;
  entriesByPathRef: RefObject<Record<string, FileEntry[]>>;
  expandedRef: RefObject<string[]>;
  fileCursorRef: RefObject<number>;
  fileOpenRequestRef: RefObject<number>;
  fileRef: RefObject<FilePayload | null>;
  interactionModeRef: RefObject<"main" | "drawer">;
  invalidTreeCacheKeysRef: RefObject<Set<string>>;
  mainDirectoryErrorRef: RefObject<string>;
  mainEntriesRef: RefObject<FileEntry[]>;
  mainViewRef: RefObject<MainViewMode>;
  managedRootByIdRef: RefObject<Record<string, ManagedRootPayload>>;
  pluginBypassRef: RefObject<boolean>;
  pluginManagerRef: RefObject<PluginManager>;
  rootEntriesRef: RefObject<FileEntry[]>;
  setCurrentRootId: Dispatch<SetStateAction<string | null>>;
  setEntriesByPath: Dispatch<SetStateAction<Record<string, FileEntry[]>>>;
  setExpanded: Dispatch<SetStateAction<string[]>>;
  setFile: Dispatch<SetStateAction<FilePayload | null>>;
  setGitDiff: Dispatch<SetStateAction<GitDiffPayload | null>>;
  setInteractionMode: Dispatch<SetStateAction<"main" | "drawer">>;
  setIsLeftOpen: Dispatch<SetStateAction<boolean>>;
  setMainDirectoryError: Dispatch<SetStateAction<string>>;
  setMainEntries: Dispatch<SetStateAction<FileEntry[]>>;
  setPluginBypass: Dispatch<SetStateAction<boolean>>;
  setPluginQuery: Dispatch<SetStateAction<Record<string, string>>>;
  setRelatedSelectedFileKey: Dispatch<SetStateAction<string>>;
  setSelectedDir: Dispatch<SetStateAction<string | null>>;
  setSelectedDirKey: Dispatch<SetStateAction<string | null>>;
  setDrawerOpenForRoot: (rootID: string | null | undefined, open: boolean) => void;
  resetLocksForRootTransition: (targetRoot: string | null | undefined) => void;
  switchMainView: (mode: MainViewMode) => void;
  replaceURLState: (next: URLState) => void;
  formatDirectoryLoadError: (message?: string | null) => string;
  treeCacheKey: (rootID: string, dirPath: string) => string;
  refreshTreeDir: (rootID: string, dirPath: string, syncMain: boolean) => Promise<void>;
  loadSessionsForRoot: (
    rootID: string,
    options?: {
      beforeTime?: string;
      afterTime?: string;
      replace?: boolean;
      force?: boolean;
    },
  ) => Promise<void>;
  handleSelectSession: (
    session: any,
    options?: { preserveTaskSelection?: boolean; preserveMainView?: boolean },
  ) => Promise<void>;
  tryShowBoundSessionForRoot: (
    rootID: string | null | undefined,
    options?: {
      pluginQuery?: Record<string, string>;
      closeLeftSidebar?: boolean;
    },
  ) => Promise<boolean>;
  rememberCurrentFileScroll: () => void;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  selectRootNode: (rootId: string, nodeId?: string) => void;
};

export function useActionHandlers(params: ActionHandlersParams): {
  handlers: ActionHandlers;
} {
  const { isMobile } = useResponsive();
  const { t } = useI18n();
  const {
    actionHandlersRef,
    fileEditStore,
    currentRootIdRef,
    currentRootNodeIdRef,
    entriesByPathRef,
    expandedRef,
    fileCursorRef,
    fileOpenRequestRef,
    fileRef,
    interactionModeRef,
    invalidTreeCacheKeysRef,
    mainDirectoryErrorRef,
    mainEntriesRef,
    mainViewRef,
    managedRootByIdRef,
    pluginBypassRef,
    pluginManagerRef,
    rootEntriesRef,
    setCurrentRootId,
    setEntriesByPath,
    setExpanded,
    setFile,
    setGitDiff,
    setInteractionMode,
    setIsLeftOpen,
    setMainDirectoryError,
    setMainEntries,
    setPluginBypass,
    setPluginQuery,
    setRelatedSelectedFileKey,
    setSelectedDir,
    setSelectedDirKey,
    setDrawerOpenForRoot,
    resetLocksForRootTransition,
    switchMainView,
    replaceURLState,
    formatDirectoryLoadError,
    treeCacheKey,
    refreshTreeDir,
    loadSessionsForRoot,
    handleSelectSession,
    tryShowBoundSessionForRoot,
    rememberCurrentFileScroll,
    getNodeIdForRoot,
    selectRootNode,
  } = params;

  const handlers = useMemo(
    () => ({
      open: async (params: any) => {
        const requestId = ++fileOpenRequestRef.current;
        if (!params?.preserveRelatedSelection) {
          setRelatedSelectedFileKey("");
        }
        const isStale = () => fileOpenRequestRef.current !== requestId;
        const parsedLocation = parseFileLocation(String(params.path || ""));
        const root = params.root || currentRootIdRef.current;
        const rootInfo = root
          ? managedRootByIdRef.current[String(root)]
          : undefined;
        const path = normalizePathForRoot(
          parsedLocation.path,
          rootInfo?.root_path,
        );
        if (!path || !root) return;
        // 深链恢复时主面板模式以 URL 的 view 为准，不因为「恢复了文件」就把面板
        // 硬拉回文件态（与 handleSelectSession 的 preserveMainView 同一约定）。
        if (!params?.preserveMainView) {
          switchMainView("files");
        }
        rememberCurrentFileScroll();
        setGitDiff(null);
        const currentFilePath = fileRef.current?.path || "";
        const currentFileRoot =
          fileRef.current?.root || currentRootIdRef.current || "";
        const isFileSwitch =
          currentFilePath !== String(path) || currentFileRoot !== String(root);
        if (isFileSwitch) {
          pluginBypassRef.current = false;
          setPluginBypass(false);
          // Only tear down the current file view when switching to a different file/root.
          // Reopening the same file (for example from session view back to file view) should
          // preserve the existing scroll position and DOM state until fresh content arrives.
          setFile(null);
        }
        resetLocksForRootTransition(root);
        if (currentRootIdRef.current !== root) {
          setCurrentRootId(root);
          selectRootNode(
            String(root),
            String(params.nodeId || "") ||
              getNodeIdForRoot(String(root)) ||
              undefined,
          );
        }
        setSelectedDir(String(root));
        setSelectedDirKey(null);
        const requestedCursor = normalizeCursor(params.cursor);
        const cursor = requestedCursor === null ? 0 : requestedCursor;
        const preserveQuery = !!params.preservePluginQuery;
        const pqNodeId = getNodeIdForRoot(String(root));
        const persistedQuery = loadPersistedPluginQuery(
          String(root),
          String(path),
          pqNodeId,
        );
        const urlQuery = preserveQuery
          ? parsePluginQuery(window.location.search)
          : {};
        // Priority: URL query > localStorage persisted query.
        const nextPluginQuery = preserveQuery
          ? { ...persistedQuery, ...urlQuery }
          : persistedQuery;
        setPluginQuery(nextPluginQuery);
        replaceURLState({
          root,
          file: path,
          session: "",
          cursor,
          pluginQuery: nextPluginQuery,
        });
        persistPluginQuery(String(root), String(path), nextPluginQuery, pqNodeId);

        const expandAndLoadTreeForFile = async () => {
          const dirs = parentDirsOfFile(String(path));
          const fileNodeId = getNodeIdForRoot(String(root));
          setExpanded((prev) => {
            const next = new Set(prev);
            next.add(scopeKey(fileNodeId ?? "", String(root)));
            dirs.forEach((dir) =>
              next.add(dirSelKey(fileNodeId, String(root), dir, false)),
            );
            return Array.from(next);
          });
          const toLoad = [".", ...dirs];
          for (const dir of toLoad) {
            const cacheKey = treeCacheKey(String(root), dir);
            if (
              Object.prototype.hasOwnProperty.call(
                entriesByPathRef.current,
                cacheKey,
              ) &&
              !invalidTreeCacheKeysRef.current.has(cacheKey)
            ) {
              continue;
            }
            try {
              const payload = await apiProtectedJSON<any>(
                appURL(
                  "/api/tree",
                  new URLSearchParams({ root: String(root), dir }),
                  getNodeIdForRoot(String(root)),
                ),
              );
              const parsed = normalizeTreeResponse(payload);
              invalidTreeCacheKeysRef.current.delete(cacheKey);
              setEntriesByPath((prev) => ({
                ...prev,
                [cacheKey]: parsed.entries,
              }));
            } catch {}
          }
        };

        void expandAndLoadTreeForFile();
        try {
          const fetchFileWithMode = async (
            mode: "full" | "incremental",
            timeoutMs?: number,
          ) => {
            const editing = fileEditStore.get(fileEditKey(String(root), String(path)));
            if (editing?.file) return editing.file;
            return fetchFile({
              rootId: String(root),
              path: String(path),
              nodeId: getNodeIdForRoot(String(root)),
              readMode: mode,
              cursor,
              timeoutMs,
            });
          };

          let readMode: "incremental" | "full" =
            params.readMode === "full" ? "full" : "incremental";
          let requiresFull = params.readMode === "full";
          if (params.readMode !== "full" && params.readMode !== "incremental") {
            const currentFilePath = fileRef.current?.path || "";
            const currentFileRoot =
              fileRef.current?.root || currentRootIdRef.current || "";
            const targetPath = String(path);
            const targetRoot = String(root);
            const sameFileReload =
              pluginBypassRef.current &&
              currentFilePath === targetPath &&
              currentFileRoot === targetRoot;

            if (sameFileReload) {
              readMode = "incremental";
            } else {
              try {
                const plugin = pluginManagerRef.current.match(
                  root,
                  buildMatchInputFromPath(path, nextPluginQuery),
                );
                readMode = inferReadModeFromPlugin(plugin);
                requiresFull = readMode === "full";
              } catch {
                readMode = "incremental";
              }
            }
          }
          const cached = await getCachedFile({
            rootId: String(root),
            path: String(path),
            readMode,
            cursor,
          });
          if (cached && !isStale()) {
            setFile({
              ...cached,
              targetLine: parsedLocation.targetLine,
              targetColumn: parsedLocation.targetColumn,
            });
          }

          let next: FilePayload | null;
          try {
            next = await fetchFileWithMode(
              readMode,
              requiresFull ? undefined : readMode === "full" ? 1500 : undefined,
            );
          } catch (err) {
            if (readMode === "full" && !requiresFull) {
              next = await fetchFileWithMode("incremental");
              readMode = "incremental";
            } else {
              throw err;
            }
          }
          if (isStale()) {
            return;
          }
          if (next) {
            setFile({
              ...next,
              targetLine: parsedLocation.targetLine,
              targetColumn: parsedLocation.targetColumn,
            });
          }
          fileCursorRef.current = cursor;
          // 打开文件不解除会话选中：面板切到文件态而已，切回对话仍应看到原来那个会话。
          setDrawerOpenForRoot(root, false);
          if (isMobile) setIsLeftOpen(false);
        } catch (err) {
          console.error("[file.open] failed", { root, path, cursor, err });
        }
      },
      open_dir: async (params: any) => {
        fileOpenRequestRef.current += 1;
        const path = params.path,
          rootParam = params.root || currentRootIdRef.current,
          isToggle = !!params.toggle,
          forceDirectory = !!params.forceDirectory,
          suppressTreeExpand = !!params.suppressTreeExpand;
        if (!path || !rootParam) return;
        setGitDiff(null);
        const isActuallyRoot = params.isRoot === true;
        const root = isActuallyRoot ? path : rootParam;
        // 多节点同名项目：根级/换根打开时携带节点信息并应用为当前选中节点
        const resolvedNodeId =
          String(params.nodeId || "").trim() ||
          (root === String(currentRootIdRef.current || "")
            ? String(currentRootNodeIdRef.current || "")
            : "") ||
          getNodeIdForRoot(root) ||
          "";
        if (
          isActuallyRoot ||
          root !== String(currentRootIdRef.current || "")
        ) {
          selectRootNode(root, resolvedNodeId || undefined);
        }
        const expandedKey = expandKey(resolvedNodeId, root, path, isActuallyRoot);
        const preserveCollapsedRoot =
          isActuallyRoot &&
          suppressTreeExpand &&
          !expandedRef.current.includes(expandedKey);
        const restoreSuppressedRootExpansion = () => {
          if (!preserveCollapsedRoot) {
            return;
          }
          setExpanded((prev) => prev.filter((k) => k !== expandedKey));
        };
        const preserveQuery = !!params.preservePluginQuery;
        const nextPluginQuery = preserveQuery
          ? parsePluginQuery(window.location.search)
          : {};
        const loadDirectoryView = async (
          targetPath: string,
          targetIsRoot: boolean,
        ) => {
          const apiDir = targetIsRoot ? "." : targetPath;
          const cacheKey = treeCacheKey(root, apiDir);
          resetLocksForRootTransition(root);
          if (currentRootIdRef.current !== root) {
            // 换项目也算一次切换操作：关掉旧根的悬浮框，别留在 per-root 记忆里
            setDrawerOpenForRoot(currentRootIdRef.current, false);
            interactionModeRef.current = "main";
            setInteractionMode("main");
            setCurrentRootId(root);
          }
          // 只有「用户在左树里点文件夹/文件名」才切到文件列表；程序化打开目录一律保持当前模式
          if (params.switchToFiles === true) switchMainView("files");
          setFile(null);
          // 会话选中不在这里清：换根由上面 resetLocksForRootTransition 负责，
          // 同根内的目录切换必须保持选中（面板切走不解除选中）。
          setMainEntries([]);
          setMainDirectoryError("");
          setPluginQuery(nextPluginQuery);
          replaceURLState({
            root,
            file: "",
            session: "",
            cursor: 0,
            pluginQuery: nextPluginQuery,
          });
          const cachedEntries = entriesByPathRef.current[cacheKey];
          if (
            Object.prototype.hasOwnProperty.call(
              entriesByPathRef.current,
              cacheKey,
            ) &&
            !invalidTreeCacheKeysRef.current.has(cacheKey)
          ) {
            setMainEntries(cachedEntries || []);
            setMainDirectoryError("");
            setSelectedDir(targetPath);
            setSelectedDirKey(
              buildDirectorySelectionKey(resolvedNodeId, root, targetPath, targetIsRoot),
            );
            setFile(null);
            fileCursorRef.current = 0;
            setDrawerOpenForRoot(root, false);
            if (!isToggle && isMobile) setIsLeftOpen(false);
            return;
          }
          try {
            const payload = await apiProtectedJSON<any>(
              appURL("/api/tree", new URLSearchParams({ root, dir: apiDir }), getNodeIdForRoot(root)),
            );
            const parsed = normalizeTreeResponse(payload);
            invalidTreeCacheKeysRef.current.delete(cacheKey);
            setMainDirectoryError("");
            setEntriesByPath((prev) => ({
              ...prev,
              [cacheKey]: parsed.entries,
            }));
            setMainEntries(parsed.entries);
            setSelectedDir(targetPath);
            setSelectedDirKey(
              buildDirectorySelectionKey(resolvedNodeId, root, targetPath, targetIsRoot),
            );
            setFile(null);
            fileCursorRef.current = 0;
            setDrawerOpenForRoot(root, false);
            if (!isToggle && isMobile) setIsLeftOpen(false);
          } catch (error) {
            if (error instanceof ProtectedAPIError) {
              const message = formatDirectoryLoadError(
                typeof error.payload?.error === "string" ? error.payload.error : "",
              );
              setSelectedDir(targetPath);
              setSelectedDirKey(
                buildDirectorySelectionKey(resolvedNodeId, root, targetPath, targetIsRoot),
              );
              setMainEntries([]);
              setMainDirectoryError(message);
              reportError("file.read_failed", message);
              return;
            }
            const message = t("directory.loadFailedRetry");
            setSelectedDir(targetPath);
            setSelectedDirKey(
              buildDirectorySelectionKey(resolvedNodeId, root, targetPath, targetIsRoot),
            );
            setMainEntries([]);
            setMainDirectoryError(message);
            reportError("file.read_failed", message);
          }
        };
        const isExpanded = expandedRef.current.includes(expandedKey);
        const isCurrentExpandedRoot =
          isActuallyRoot && isExpanded && currentRootIdRef.current === root;
        if (
          isToggle &&
          ((!isActuallyRoot && isExpanded) || isCurrentExpandedRoot)
        ) {
          setExpanded((prev) => prev.filter((k) => k !== expandedKey));
          if (!isActuallyRoot) {
            const parentDir = dirnameOfPath(path);
            const parentPath = parentDir === "." ? root : parentDir;
            await loadDirectoryView(parentPath, parentDir === ".");
          }
          return;
        }
        if (isActuallyRoot) {
          resetLocksForRootTransition(path);
          setCurrentRootId(path);
          // 根树互斥：打开/激活一个根，收起其它根的展开树（只保留根自身键，子目录键原样保留）
          const otherRootKeys = new Set(
            rootEntriesRef.current
              .filter((entry) => entry.is_root === true && entry.path !== root)
              .map((entry) =>
                expandKey(String((entry as any)._nodeId || ""), entry.path, entry.path, true),
              ),
          );
          if (!suppressTreeExpand) {
            setExpanded((prev) => {
              const kept = prev.filter((k) => !otherRootKeys.has(k));
              const rootKey = expandKey(resolvedNodeId, path, path, true);
              // 换根是「减一个加一个」：长度可能不变，必须按内容判断是否已就位，
              // 不能用 prev.length===next.length 当 identity 守卫（会吞掉换根更新）。
              if (kept.length === prev.length && kept.includes(rootKey)) {
                return prev;
              }
              return Array.from(new Set([...kept, rootKey]));
            });
          } else {
            setExpanded((prev) => {
              const next = prev.filter((k) => !otherRootKeys.has(k));
              return prev.length === next.length ? prev : next;
            });
          }
          if (!forceDirectory) {
            const restored = await tryShowBoundSessionForRoot(path, {
              pluginQuery: nextPluginQuery,
              closeLeftSidebar: !isToggle,
            });
            if (restored) {
              void loadSessionsForRoot(path, { replace: true, force: true });
              if (mainViewRef.current === "files" && !mainEntriesRef.current.length && !mainDirectoryErrorRef.current) {
                // 文件态没有内容可显示：补上项目顶层目录，行为与手动切面板一致
                await actionHandlersRef.current.open_dir({
                  path,
                  root: path,
                  isRoot: true,
                  forceDirectory: true,
                });
              } else {
                await refreshTreeDir(path, ".", false);
              }
              restoreSuppressedRootExpansion();
              return;
            }
          }
        } else {
          setExpanded((prev) => Array.from(new Set([...prev, expandedKey])));
        }
        await loadDirectoryView(path, isActuallyRoot);
        restoreSuppressedRootExpansion();
      },    }),
    [
      isMobile,
      normalizeTreeResponse,
      switchMainView,
      setDrawerOpenForRoot,
      replaceURLState,
      rememberCurrentFileScroll,
      treeCacheKey,
      tryShowBoundSessionForRoot,
      loadSessionsForRoot,
      resetLocksForRootTransition,
    ],
  );

  return { handlers };
}
