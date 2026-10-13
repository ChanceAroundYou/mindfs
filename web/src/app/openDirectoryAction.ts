import type { ActionHandlersParams } from "./useActionHandlers";
import {
  buildDirectorySelectionKey,
  dirnameOfPath,
  parsePluginQuery,
} from "./appPath";
import { normalizeTreeResponse } from "./appUtils";
import { ProtectedAPIError, protectedJSON as apiProtectedJSON } from "../services/net/api";
import { appURL } from "../services/net/base";
import { reportError } from "../services/net/error";
import { dirSelKey, expandKey } from "../shared/scope";

export function createOpenDirectoryAction(
  params: ActionHandlersParams,
  isMobile: boolean,
  t: (key: "directory.loadFailedRetry") => string,
) {
  const {
    actionHandlersRef, currentRootIdRef, currentRootNodeIdRef, entriesByPathRef, expandedRef,
    fileCursorRef, fileOpenRequestRef, interactionModeRef, invalidTreeCacheKeysRef,
    mainDirectoryErrorRef, mainEntriesRef, mainViewRef, rootEntriesRef,
    setCurrentRootId, setEntriesByPath, setExpanded, setFile, setGitDiff,
    setInteractionMode, setIsLeftOpen, setMainDirectoryError, setMainEntries,
    setPluginQuery, setSelectedDir, setSelectedDirKey, setDrawerOpenForRoot,
    resetLocksForRootTransition, switchMainView, replaceURLState, treeCacheKey, refreshTreeDir, loadSessionsForRoot,
    tryShowBoundSessionForRoot, formatDirectoryLoadError, getNodeIdForRoot,
    selectRootNode,
  } = params;
  return async (params: any) => {
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
      };
}
