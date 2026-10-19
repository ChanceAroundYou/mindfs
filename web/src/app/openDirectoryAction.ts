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
import { dirSelKey, expandKey, treeKey } from "../shared/scope";

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
        // 纯展开一个根时只需要它的顶层目录条目 —— 按**该根所属的节点**去取（跨节点
        // 项目不能打当前节点）。缓存键用 treeKey(nodeId, root, ".")：与文件树里根行
        // 的子节点键（childEntryKey）同源，否则拉回来的条目落进别的桶、树里看不见。
        const ensureRootEntriesLoaded = async (targetRoot: string, nodeId: string) => {
          const effectiveNodeId =
            String(nodeId || "").trim() || getNodeIdForRoot(targetRoot) || "";
          const cacheKey = treeKey(effectiveNodeId, targetRoot, ".");
          if (
            Object.prototype.hasOwnProperty.call(entriesByPathRef.current, cacheKey) &&
            !invalidTreeCacheKeysRef.current.has(cacheKey)
          ) {
            return;
          }
          try {
            const payload = await apiProtectedJSON<any>(
              appURL(
                "/api/tree",
                new URLSearchParams({ root: targetRoot, dir: "." }),
                effectiveNodeId,
              ),
            );
            const parsed = normalizeTreeResponse(payload);
            invalidTreeCacheKeysRef.current.delete(cacheKey);
            setEntriesByPath((prev) => ({ ...prev, [cacheKey]: parsed.entries }));
          } catch {
            // 拉不到就当没展开：树空着，不把错误抛到 UI 上。
          }
        };
        const expandedKey = expandKey(resolvedNodeId, root, path, isActuallyRoot);
        const isExpanded = expandedRef.current.includes(expandedKey);
        // 根行的箭头 = **纯展开/收起**：不切当前项目、不换选中节点、不进主面板、
        // 不恢复绑定会话。点项目名才是「进入项目」（onSelectRoot 那条路）。
        //
        // 以前根行没有独立分支，跨节点时点箭头会一路落到下面「打开这个根」那段
        // （selectRootNode + 换根 + tryShowBoundSessionForRoot）—— 表现为「收起」
        // 永远不生效，倒像先进了项目，得再点一下才收得起（2026-10-20 用户实测）。
        if (isToggle && isActuallyRoot) {
          if (isExpanded) {
            setExpanded((prev) => prev.filter((k) => k !== expandedKey));
            return;
          }
          setExpanded((prev) => Array.from(new Set([...prev, expandedKey])));
          await ensureRootEntriesLoaded(root, resolvedNodeId);
          return;
        }
        if (
          isActuallyRoot ||
          root !== String(currentRootIdRef.current || "")
        ) {
          selectRootNode(root, resolvedNodeId || undefined);
        }
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
