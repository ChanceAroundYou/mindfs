/**
 * 项目（托管根）管理 —— A4 簇（5 个单元，~560 行）。
 *
 * 覆盖：根作用域客户端状态清理、项目显示名重命名、托管根刷新与载荷加载、
 * 当前根重命名。
 *
 * 做法：hook，参数面 UseManagedRootsParams 即完整契约；函数体逐字搬入
 * （保持源码正则断言可命中，配合 source-map 注册）。
 */
import { useCallback, useMemo, type MutableRefObject, type Dispatch, type SetStateAction } from "react";
import { ManagedRootPayload, indexManagedRoots, managedDirAddErrorMessage, mapManagedRootsToEntries, useResponsive } from "./appMisc";
import { URLState, comparableManagedRootPath, rootNodeKey } from "./appPath";
import { MultiProjectSessionGroup, PendingSend, SessionItem, SessionQueueItem, SlashCommandResult } from "./appSession";
import { LAST_ROOT_NODE_STORAGE_KEY, LAST_ROOT_STORAGE_KEY, PLUGIN_QUERY_STORAGE_PREFIX, accountScopedKey, loadLastRootId, loadLastRootNodeId, persistFileScrollPositions, removeLocalStorageByPrefix } from "./appStorage";
import { type ActionHandlers, useActionHandlers } from "./useActionHandlers";
import { useCompletionSound } from "./useCompletionSound";
import { useExternalSessionImport } from "./useExternalSessionImport";
import { useGitActions } from "./useGitActions";
import { useGitData } from "./useGitData";
import { useMultiProjectSessions } from "./useMultiProjectSessions";
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
import { type FilePayload, clearFileCacheForRoot } from "../services/file";
import { clearGitHistoryCache } from "../services/git";
import { protectedJSON as apiProtectedJSON, withNodeRetry } from "../services/net/api";
import { appPath } from "../services/net/base";
import { type BootstrapState, bootstrapService } from "../services/net/bootstrap";
import { reportError } from "../services/net/error";
import { getNodes, migrateLegacySingleBase, syncNodesFromServer } from "../services/net/nodeRegistry";
import { setRootNodeMap } from "../services/net/rootNode";
import { type DirectorySortMode, type FileEntry } from "../services/prefs/directorySort";
import { clearCachedSessionsForRoot } from "../services/session";
import { scopeKey } from "../shared/scope";

export type UseManagedRootsParams = {
  actionHandlersRef: MutableRefObject<ActionHandlers>;
  bootstrapState: BootstrapState;
  boundSessionByRootRef: MutableRefObject<Record<string, string | null>>;
  bumpCacheVersion: () => any;
  cancelRequestedBySessionRef: MutableRefObject<Record<string, boolean>>;
  currentRootIdRef: MutableRefObject<string | null>;
  currentRootNodeIdRef: MutableRefObject<string | null>;
  drawerOpenByRootRef: MutableRefObject<Record<string, boolean>>;
  drawerSessionByRootRef: MutableRefObject<Record<string, SessionItem | null>>;
  entriesByPathRef: MutableRefObject<Record<string, FileEntry[]>>;
  file: FilePayload | null;
  fileScrollPositionsRef: MutableRefObject<Record<string, number>>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  invalidTreeCacheKeysRef: MutableRefObject<Set<string>>;
  loadMultiProjectSessionGroups: ReturnType<typeof useMultiProjectSessions>["loadMultiProjectSessionGroups"];
  loadedSessionRef: ReturnType<typeof useSessionCache>["loadedSessionRef"];
  loadingSessionRef: ReturnType<typeof useSessionCache>["loadingSessionRef"];
  managedRootByIdRef: MutableRefObject<Record<string, ManagedRootPayload>>;
  managedRootByKeyRef: MutableRefObject<Record<string, ManagedRootPayload>>;
  managedRootIdsRef: MutableRefObject<Set<string>>;
  managedRootsRequestRef: MutableRefObject<Promise<ManagedRootPayload[] | null> | null>;
  multiProjectPendingRef: MutableRefObject<Record<string, boolean>>;
  multiProjectSessionsEnabled: any;
  nodeLoadFailuresRef: MutableRefObject<Array<{ id: string; name: string }>>;
  notifiedNodeFailuresRef: MutableRefObject<Set<string>>;
  notifyNodeLoadFailedRef: MutableRefObject<(node: { id: string; name: string }) => void>;
  optimisticDequeuedIdsRef: MutableRefObject<Record<string, Set<string>>>;
  pendingBySessionRef: MutableRefObject<Record<string, PendingSend>>;
  pendingRequestRef: MutableRefObject<Record<string, PendingSend>>;
  pluginQuery: Record<string, string>;
  pluginsLoadedByRootRef: MutableRefObject<Record<string, boolean>>;
  pluginsLoadingByRootRef: MutableRefObject<Record<string, Promise<void>>>;
  pluginsTrustPendingByRootRef: MutableRefObject<Record<string, boolean>>;
  queueFrozenBySessionRef: MutableRefObject<Record<string, boolean>>;
  queuedMessagesBySessionRef: MutableRefObject<Record<string, SessionQueueItem[]>>;
  refreshMultiProjectReplyingSessions: () => Promise<any>;
  replaceURLState: (next: URLState) => any;
  scopedRootKey: (rootId: string) => string;
  selectedSessionByRootRef: MutableRefObject<Record<string, string | null>>;
  sessionCacheRef: ReturnType<typeof useSessionCache>["sessionCacheRef"];
  setActiveBoundSessionKey: Dispatch<SetStateAction<string | null>>;
  setCurrentRootId: Dispatch<SetStateAction<string | null>>;
  setCurrentSession: Dispatch<SetStateAction<SessionItem | null>>;
  setDirectorySortOverrides: Dispatch<SetStateAction<Record<string, DirectorySortMode>>>;
  setEntriesByPath: Dispatch<SetStateAction<Record<string, FileEntry[]>>>;
  setFile: Dispatch<SetStateAction<FilePayload | null>>;
  setGitHistoryExpandedByRoot: ReturnType<typeof useGitData>["setGitHistoryExpandedByRoot"];
  setGitStatusExpandedByRoot: ReturnType<typeof useGitData>["setGitStatusExpandedByRoot"];
  setInteractionMode: Dispatch<SetStateAction<"main" | "drawer">>;
  setIsDrawerOpen: Dispatch<SetStateAction<boolean>>;
  setMainEntries: Dispatch<SetStateAction<FileEntry[]>>;
  setManagedRootIds: Dispatch<SetStateAction<string[]>>;
  setMultiProjectPendingByKey: Dispatch<SetStateAction<Record<string, boolean>>>;
  setMultiProjectSessionGroups: Dispatch<SetStateAction<MultiProjectSessionGroup[]>>;
  setQueueVersion: Dispatch<SetStateAction<number>>;
  setRootEntries: Dispatch<SetStateAction<FileEntry[]>>;
  setSelectedDir: Dispatch<SetStateAction<string | null>>;
  setSelectedDirKey: Dispatch<SetStateAction<string | null>>;
  setSelectedSession: Dispatch<SetStateAction<SessionItem | null>>;
  setSelectedSessionLoading: Dispatch<SetStateAction<boolean>>;
  setSessions: Dispatch<SetStateAction<SessionItem[]>>;
  setSlashCommandResults: Dispatch<SetStateAction<Record<string, SlashCommandResult>>>;
  staleSessionKeysRef: ReturnType<typeof useSessionCache>["staleSessionKeysRef"];
  suppressedAutoBindSessionByRootRef: MutableRefObject<Record<string, string | null>>;
};

export function useManagedRoots(params: UseManagedRootsParams) {
  const { t } = useI18n();
  const {
    actionHandlersRef,
    bootstrapState,
    boundSessionByRootRef,
    bumpCacheVersion,
    cancelRequestedBySessionRef,
    currentRootIdRef,
    currentRootNodeIdRef,
    drawerOpenByRootRef,
    drawerSessionByRootRef,
    entriesByPathRef,
    file,
    fileScrollPositionsRef,
    getNodeIdForRoot,
    invalidTreeCacheKeysRef,
    loadMultiProjectSessionGroups,
    loadedSessionRef,
    loadingSessionRef,
    managedRootByIdRef,
    managedRootByKeyRef,
    managedRootIdsRef,
    managedRootsRequestRef,
    multiProjectPendingRef,
    multiProjectSessionsEnabled,
    nodeLoadFailuresRef,
    notifiedNodeFailuresRef,
    notifyNodeLoadFailedRef,
    optimisticDequeuedIdsRef,
    pendingBySessionRef,
    pendingRequestRef,
    pluginQuery,
    pluginsLoadedByRootRef,
    pluginsLoadingByRootRef,
    pluginsTrustPendingByRootRef,
    queueFrozenBySessionRef,
    queuedMessagesBySessionRef,
    refreshMultiProjectReplyingSessions,
    replaceURLState,
    scopedRootKey,
    selectedSessionByRootRef,
    sessionCacheRef,
    setActiveBoundSessionKey,
    setCurrentRootId,
    setCurrentSession,
    setDirectorySortOverrides,
    setEntriesByPath,
    setFile,
    setGitHistoryExpandedByRoot,
    setGitStatusExpandedByRoot,
    setInteractionMode,
    setIsDrawerOpen,
    setMainEntries,
    setManagedRootIds,
    setMultiProjectPendingByKey,
    setMultiProjectSessionGroups,
    setQueueVersion,
    setRootEntries,
    setSelectedDir,
    setSelectedDirKey,
    setSelectedSession,
    setSelectedSessionLoading,
    setSessions,
    setSlashCommandResults,
    staleSessionKeysRef,
    suppressedAutoBindSessionByRootRef,
  } = params;

  const clearRootScopedClientState = useCallback((rootID: string, options?: { removeLastRoot?: boolean }) => {
    const root = String(rootID || "").trim();
    if (!root) {
      return;
    }
    const sessionPrefix = `${root}::`;
    const treePrefix = `${root}:`;

    const deleteRecordKeys = <T,>(record: Record<string, T>, predicate: (key: string) => boolean) => {
      for (const key of Object.keys(record)) {
        if (predicate(key)) {
          delete record[key];
        }
      }
    };
    const deleteSessionRecordKeys = <T,>(record: Record<string, T>) => {
      deleteRecordKeys(record, (key) => key.startsWith(sessionPrefix));
    };

    clearGitHistoryCache(root);
    clearFileCacheForRoot(root);
    void clearCachedSessionsForRoot(root, getNodeIdForRoot(root));

    delete boundSessionByRootRef.current[scopedRootKey(root)];
    delete suppressedAutoBindSessionByRootRef.current[scopedRootKey(root)];
    delete drawerSessionByRootRef.current[scopedRootKey(root)];
    delete selectedSessionByRootRef.current[scopedRootKey(root)];
    delete drawerOpenByRootRef.current[scopedRootKey(root)];
    delete pluginsLoadedByRootRef.current[scopedRootKey(root)];
    delete pluginsLoadingByRootRef.current[scopedRootKey(root)];
    delete pluginsTrustPendingByRootRef.current[scopedRootKey(root)];

    deleteSessionRecordKeys(sessionCacheRef.current);
    deleteSessionRecordKeys(loadedSessionRef.current);
    deleteSessionRecordKeys(loadingSessionRef.current);
    deleteSessionRecordKeys(pendingBySessionRef.current);
    deleteSessionRecordKeys(queuedMessagesBySessionRef.current);
    deleteSessionRecordKeys(queueFrozenBySessionRef.current);
    deleteSessionRecordKeys(cancelRequestedBySessionRef.current);
    deleteSessionRecordKeys(pendingRequestRef.current);
    deleteRecordKeys(optimisticDequeuedIdsRef.current, (key) => key.startsWith(sessionPrefix));
    staleSessionKeysRef.current = new Set(
      Array.from(staleSessionKeysRef.current).filter((key) => !key.startsWith(sessionPrefix)),
    );

    deleteRecordKeys(entriesByPathRef.current, (key) => key === root || key.startsWith(treePrefix));
    setEntriesByPath((prev) => {
      const next = { ...prev };
      deleteRecordKeys(next, (key) => key === root || key.startsWith(treePrefix));
      return next;
    });
    invalidTreeCacheKeysRef.current = new Set(
      Array.from(invalidTreeCacheKeysRef.current).filter(
        (key) => key !== root && !key.startsWith(treePrefix),
      ),
    );

    setGitStatusExpandedByRoot((prev) => {
      const scoped = scopedRootKey(root);
      if (!(scoped in prev)) return prev;
      const next = { ...prev };
      delete next[scoped];
      return next;
    });
    setGitHistoryExpandedByRoot((prev) => {
      const scoped = scopedRootKey(root);
      if (!(scoped in prev)) return prev;
      const next = { ...prev };
      delete next[scoped];
      return next;
    });
    setDirectorySortOverrides((prev) => {
      let changed = false;
      const next = { ...prev };
      for (const key of Object.keys(next)) {
        if (key === root || key.startsWith(treePrefix)) {
          delete next[key];
          changed = true;
        }
      }
      return changed ? next : prev;
    });
    setMultiProjectPendingByKey((prev) => {
      let changed = false;
      const next = { ...prev };
      for (const key of Object.keys(next)) {
        if (key.startsWith(sessionPrefix)) {
          delete next[key];
          changed = true;
        }
      }
      return changed ? next : prev;
    });
    setMultiProjectSessionGroups((prev) => {
      const next = prev.filter((group) => group.rootId !== root);
      return next.length === prev.length ? prev : next;
    });
    multiProjectPendingRef.current = Object.fromEntries(
      Object.entries(multiProjectPendingRef.current).filter(([key]) => !key.startsWith(sessionPrefix)),
    );
    setSlashCommandResults((prev) => {
      let changed = false;
      const next = { ...prev };
      for (const key of Object.keys(next)) {
        if (key.startsWith(sessionPrefix)) {
          delete next[key];
          changed = true;
        }
      }
      return changed ? next : prev;
    });

    deleteRecordKeys(fileScrollPositionsRef.current, (key) => key.startsWith(sessionPrefix));
    persistFileScrollPositions(fileScrollPositionsRef.current);
    removeLocalStorageByPrefix(`${PLUGIN_QUERY_STORAGE_PREFIX}${root}:`);
    if (
      options?.removeLastRoot === true &&
      typeof window !== "undefined" &&
      window.localStorage.getItem(accountScopedKey(LAST_ROOT_STORAGE_KEY)) === root
    ) {
      window.localStorage.removeItem(accountScopedKey(LAST_ROOT_STORAGE_KEY));
      window.localStorage.removeItem(accountScopedKey(LAST_ROOT_NODE_STORAGE_KEY));
    }

    bumpCacheVersion();
    setQueueVersion((value) => value + 1);
  }, [bumpCacheVersion]);
  const loadManagedRootPayloads = useCallback(async (opts?: { force?: boolean }) => {
    if (bootstrapService.snapshot().phase !== "ready") {
      return null;
    }
    if (!opts?.force && managedRootsRequestRef.current) {
      return managedRootsRequestRef.current;
    }
    if (opts?.force) {
      managedRootsRequestRef.current = null;
    }
    const request = (async () => {
      nodeLoadFailuresRef.current = [];
      try {
        try { migrateLegacySingleBase(); } catch {}
        // 冷启动时 getNodes() 首屏只有 local，需等待服务端节点同步后再取全量
        try {
          if (!opts?.force) {
            await syncNodesFromServer().catch(() => {});
          }
        } catch {}
        const nodes = getNodes();
        const targets = nodes;
        if (!targets.length) {
          const dirs = await apiProtectedJSON<ManagedRootPayload[]>(appPath("/api/dirs"));
          return Array.isArray(dirs) ? dirs : [];
        }
        const nodeFailures: Array<{ id: string; name: string }> = [];
        // 跨机器也带 user=（用户名，见 base.ts）：对方按用户名解析到它本地的账户，
        // 取到的确实是「我的」数据，不再是它主账户的回落。
        // 对方没有这个账户时服务端回 **200 空列表**（不是 404）——所以这里没有
        // 缺账户的特殊分支：空就是空，节点照常显示，只是没有项目。
        const results = await Promise.all(targets.map(async (n) => {
          try {
            const dirs = await withNodeRetry(() => apiProtectedJSON<ManagedRootPayload[]>(appPath("/api/dirs", n.id)));
            return (Array.isArray(dirs) ? dirs : []).map((d: any) => ({ ...d, _nodeId: (d as any)._nodeId || n.id, _nodeColor: n.color, _nodeName: n.name }));
          } catch (err) {
            nodeFailures.push({ id: String(n.id), name: String(n.name || n.id) });
            return [] as ManagedRootPayload[];
          }
        }));
        nodeLoadFailuresRef.current = nodeFailures;
        const flat = results.flat() as ManagedRootPayload[];
        const seen = new Set<string>();
        const deduped: ManagedRootPayload[] = [];
        for (const d of flat) { const id = String((d as any).id || ""); const nid = String((d as any)._nodeId || ""); const key = `${nid}::${id}`; if (!id || seen.has(key)) continue; seen.add(key); deduped.push(d); }
        return deduped;
      } catch {
        return null;
      }
    })().finally(() => {
      managedRootsRequestRef.current = null;
    });
    managedRootsRequestRef.current = request;
    return request;
  }, [bootstrapState.phase]);
  const refreshManagedRoots = useCallback(async () => {
    const dirs = await loadManagedRootPayloads();
    if (!dirs) {
      return;
    }
    // 抓取失败的节点：提示 + 给重试入口。恢复后清掉记录，下次再失败能重新提示。
    const failedNodeIds = new Set(nodeLoadFailuresRef.current.map((f) => f.id));
    for (const id of Array.from(notifiedNodeFailuresRef.current)) {
      if (!failedNodeIds.has(id)) notifiedNodeFailuresRef.current.delete(id);
    }
    for (const failed of nodeLoadFailuresRef.current) {
      notifyNodeLoadFailedRef.current(failed);
    }
    const nextDirs = Array.isArray(dirs) ? dirs : [];
    const nextRootIds = nextDirs.map((dir) => dir.id).filter(Boolean);
    const previousRootById = managedRootByIdRef.current;
    const indexed = indexManagedRoots(nextDirs as ManagedRootPayload[]);
    const nextRootById = indexed.byId;
    let clearedRootScopedState = false;
    for (const rootID of Object.keys(previousRootById)) {
      if (!(rootID in nextRootById)) {
        clearRootScopedClientState(rootID, { removeLastRoot: true });
        clearedRootScopedState = true;
      }
    }
    for (const rootID of nextRootIds) {
      const previousPath = comparableManagedRootPath(previousRootById[rootID]?.root_path);
      const nextPath = comparableManagedRootPath(nextRootById[rootID]?.root_path);
      if (previousPath && nextPath && previousPath !== nextPath) {
        clearRootScopedClientState(rootID);
        clearedRootScopedState = true;
      }
    }
    managedRootByKeyRef.current = { ...managedRootByKeyRef.current, ...indexed.byKey };
    // D2: 保留当前选中根的 bare 槽位，避免同名项目被本地蓝覆盖（瞬时全蓝根因）
    {
      const rid = String(currentRootIdRef.current || "").trim();
      const curNid = String(currentRootNodeIdRef.current || "").trim();
      if (rid && curNid) {
        const scoped = scopeKey(curNid, rid);
        const keep = (indexed.byKey as any)[scoped] || (managedRootByKeyRef.current as any)[scoped];
        if (keep) nextRootById[rid] = keep;
      }
    }
    managedRootByIdRef.current = nextRootById;
    setRootNodeMap(managedRootByIdRef.current as Record<string, any>);
    {
      const curRid = String(currentRootIdRef.current || "").trim();
      const curNid = String(currentRootNodeIdRef.current || "").trim();
      const curSlot = curRid && curNid ? `${curNid}::${curRid}` : "(none)";
    }
    // 任何索引变化（新增节点/项目/路径）都应重拉多节点会话，确保 PC 等远端分组首屏即出现
    // 之前仅在 cleared/keySetChanged 时触发，导致清缓存冷启动 PC 迟迟不出现、需点会话才补齐
    if (multiProjectSessionsEnabled) {
      void refreshMultiProjectReplyingSessions();
      void loadMultiProjectSessionGroups();
    }

    managedRootIdsRef.current = new Set(nextRootIds);
    setManagedRootIds(nextRootIds);
    setRootEntries(mapManagedRootsToEntries(nextDirs));
    Object.keys(selectedSessionByRootRef.current).forEach((rootID) => {
      if (!nextRootIds.includes(rootID)) {
        delete selectedSessionByRootRef.current[scopedRootKey(rootID)];
      }
    });

    if (nextRootIds.length === 0) {
      setCurrentRootId(null);
      setSelectedDir(null);
      setSelectedDirKey(null);
      setMainEntries([]);
      setFile(null);
      setSelectedSession(null);
      setSelectedSessionLoading(false);
      setSessions([]);
      setCurrentSession(null);
      setActiveBoundSessionKey(null);
      selectedSessionByRootRef.current = {};
      setInteractionMode("main");
      setIsDrawerOpen(false);
      replaceURLState({
        root: "",
        file: "",
        session: "",
        cursor: 0,
        pluginQuery: {},
      });
      return;
    }

    const currentRoot = currentRootIdRef.current;
    if (currentRoot && nextRootIds.includes(currentRoot)) {
      return;
    }

    const lastRoot = loadLastRootId();
    const lastNode = loadLastRootNodeId();
    const nextRoot =
      lastRoot && nextRootIds.includes(lastRoot) ? lastRoot : nextRootIds[0];
    const preferredDir =
      nextDirs.find(
        (d) => d.id === nextRoot && (!lastNode || (d as any)._nodeId === lastNode),
      ) ||
      nextDirs.find((d) => d.id === nextRoot);
    await actionHandlersRef.current.open_dir({
      path: nextRoot,
      root: nextRoot,
      preservePluginQuery: true,
      isRoot: true,
      nodeId: (preferredDir as any)?._nodeId || undefined,
    });
  }, [
    clearRootScopedClientState,
    loadManagedRootPayloads,
    loadMultiProjectSessionGroups,
    multiProjectSessionsEnabled,
    refreshMultiProjectReplyingSessions,
    replaceURLState,
  ]);
  const applyManagedRootRename = useCallback(
    (oldRootID: string, rootPayload: ManagedRootPayload | null | undefined) => {
      const oldID = String(oldRootID || "").trim();
      const nextID = String(rootPayload?.id || "").trim();
      if (!oldID || !nextID) {
        return false;
      }

      const previousRoot =
        managedRootByIdRef.current[oldID] ||
        managedRootByIdRef.current[nextID] ||
        ({} as ManagedRootPayload);
      const nextRoot = {
        ...previousRoot,
        ...rootPayload,
        id: nextID,
      } as ManagedRootPayload;

      const nextRootById = { ...managedRootByIdRef.current };
      delete nextRootById[oldID];
      nextRootById[nextID] = nextRoot;
      managedRootByIdRef.current = nextRootById;
      setRootNodeMap(nextRootById as Record<string, any>);

      const moveRecordKey = <T,>(record: Record<string, T>) => {
        if (oldID === nextID) {
          return;
        }
        for (const k of Object.keys(record)) {
          const idx = k.lastIndexOf("::");
          const rid = idx >= 0 ? k.slice(idx + 2) : k;
          if (rid !== oldID) {
            continue;
          }
          const nextKey = idx >= 0 ? `${k.slice(0, idx + 2)}${nextID}` : nextID;
          record[nextKey] = record[k];
          delete record[k];
        }
      };
      moveRecordKey(boundSessionByRootRef.current);
      moveRecordKey(suppressedAutoBindSessionByRootRef.current);
      moveRecordKey(drawerSessionByRootRef.current);
      moveRecordKey(selectedSessionByRootRef.current);
      moveRecordKey(drawerOpenByRootRef.current);
      moveRecordKey(pluginsLoadedByRootRef.current);
      moveRecordKey(pluginsLoadingByRootRef.current);

      const moveStateRecord = <T,>(record: Record<string, T>) => {
        if (oldID === nextID) {
          return record;
        }
        const next: Record<string, T> = {};
        for (const [k, v] of Object.entries(record)) {
          const idx = k.lastIndexOf("::");
          const rid = idx >= 0 ? k.slice(idx + 2) : k;
          const nextKey = idx >= 0 ? `${k.slice(0, idx + 2)}${nextID}` : nextID;
          next[rid === oldID ? nextKey : k] = v;
        }
        return next;
      };
      setGitStatusExpandedByRoot((prev) => moveStateRecord(prev));
      setGitHistoryExpandedByRoot((prev) => moveStateRecord(prev));

      const remapSessionCacheKey = (key: string): string | null => {
        const parts = key.split("::");
        if (parts.length === 3) {
          if (parts[1] !== oldID) return null;
          return `${parts[0]}::${nextID}::${parts.slice(2).join("::")}`;
        }
        if (parts.length === 2) {
          if (parts[0] !== oldID) return null;
          return `${nextID}::${parts[1]}`;
        }
        return null;
      };
      const moveCacheRecord = <T,>(record: Record<string, T>) => {
        if (oldID === nextID) {
          return;
        }
        for (const key of Object.keys(record)) {
          const nextKey = remapSessionCacheKey(key);
          if (nextKey === null) continue;
          record[nextKey] = record[key];
          delete record[key];
        }
      };
      moveCacheRecord(sessionCacheRef.current);
      moveCacheRecord(loadedSessionRef.current);
      if (oldID !== nextID) {
        staleSessionKeysRef.current = new Set(
          Array.from(staleSessionKeysRef.current)
            .map((key) => remapSessionCacheKey(key) ?? key),
        );
      }

      const moveTreeRecord = <T,>(record: Record<string, T>) => {
        if (oldID === nextID) {
          return record;
        }
        const next = { ...record };
        for (const key of Object.keys(record)) {
          const afterScope = key.includes("::")
            ? key.slice(key.lastIndexOf("::") + 2)
            : key;
          const idx = afterScope.indexOf(":");
          const rid = idx >= 0 ? afterScope.slice(0, idx) : afterScope;
          if (rid !== oldID) {
            continue;
          }
          const head = key.slice(0, key.length - afterScope.length);
          const nextAfter = `${nextID}${idx >= 0 ? afterScope.slice(idx) : ""}`;
          next[`${head}${nextAfter}`] = record[key];
          delete next[key];
        }
        return next;
      };
      entriesByPathRef.current = moveTreeRecord(entriesByPathRef.current);
      setEntriesByPath((prev) => moveTreeRecord(prev));
      if (oldID !== nextID) {
        invalidTreeCacheKeysRef.current = new Set(
          Array.from(invalidTreeCacheKeysRef.current).map((key) => {
            const afterScope = key.includes("::")
              ? key.slice(key.lastIndexOf("::") + 2)
              : key;
            const idx = afterScope.indexOf(":");
            const rid = idx >= 0 ? afterScope.slice(0, idx) : afterScope;
            if (rid !== oldID) return key;
            const head = key.slice(0, key.length - afterScope.length);
            return `${head}${nextID}${idx >= 0 ? afterScope.slice(idx) : ""}`;
          }),
        );
      }

      setManagedRootIds((prev) => {
        const source = prev.length ? prev : Array.from(managedRootIdsRef.current);
        let replaced = false;
        const nextIds = source.map((id) => {
          if (id !== oldID) {
            return id;
          }
          replaced = true;
          return nextID;
        });
        if (!replaced && !nextIds.includes(nextID)) {
          nextIds.push(nextID);
        }
        const deduped = Array.from(new Set(nextIds.filter(Boolean)));
        managedRootIdsRef.current = new Set(deduped);
        setRootEntries(
          mapManagedRootsToEntries(
            deduped
              .map((id) => managedRootByIdRef.current[id])
              .filter((dir): dir is ManagedRootPayload => !!dir),
          ),
        );
        return deduped;
      });
      return true;
    },
    [],
  );
  const handleRenameCurrentRoot = useCallback(
    async (nextName: string) => {
      const rootID = currentRootIdRef.current;
      const trimmedName = String(nextName || "").trim();
      if (!rootID || !trimmedName) {
        return false;
      }
      const currentDisplayName = String((managedRootByIdRef.current[rootID] as any)?.display_name || managedRootByIdRef.current[rootID]?.id || "").trim();
      if (trimmedName === currentDisplayName) {
        return true;
      }
      try {
        const renamed = await apiProtectedJSON<ManagedRootPayload>(
          appPath(`/api/dirs/${encodeURIComponent(rootID)}/display-name`),
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ display_name: trimmedName }),
          },
        );
        // Display-name rename never changes id/path: patch local map in place via refresh; keep compatibility with physical rename helper
        if (String(renamed?.id || "").trim() && String(renamed.id).trim() !== rootID) {
          applyManagedRootRename(rootID, renamed);
          await actionHandlersRef.current.open_dir({
            path: renamed.id,
            root: renamed.id,
            isRoot: true,
            forceDirectory: true,
          });
        } else {
          // Same id: merge display_name in place without shuffling per-root caches (session keys stay valid)
          const merged = { ...(managedRootByIdRef.current[rootID] || {} as ManagedRootPayload), ...(renamed || {}), id: rootID } as ManagedRootPayload;
          const nextById = { ...managedRootByIdRef.current, [rootID]: merged };
          managedRootByIdRef.current = nextById as Record<string, ManagedRootPayload>;
          const renameNid = String((merged as any)._nodeId || "").trim();
          if (renameNid) managedRootByKeyRef.current[rootNodeKey(renameNid, rootID)] = merged;
          setRootNodeMap(nextById as Record<string, any>);
          const ids = Array.from(managedRootIdsRef.current);
          setRootEntries(mapManagedRootsToEntries(ids.map((id) => nextById[id]).filter(Boolean) as ManagedRootPayload[]));
          setMultiProjectSessionGroups((prev) =>
            prev.map((g) =>
              g.rootId === rootID
                ? { ...g, rootName: String((merged as any)?.display_name || (merged as any)?.id || g.rootName || rootID) }
                : g,
            ),
          );
        }
        return true;
      } catch (err) {
        reportError(
          "root.rename_failed",
          managedDirAddErrorMessage(err, t("root.renameProjectFailed"), t),
        );
        return false;
      }
    },
    [applyManagedRootRename, t],
  );

  return {
    clearRootScopedClientState,
    loadManagedRootPayloads,
    refreshManagedRoots,
    applyManagedRootRename,
    handleRenameCurrentRoot,
  };
}
