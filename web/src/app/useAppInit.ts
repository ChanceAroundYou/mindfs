// 从 App.tsx 抽出的 useEffect（机械搬移，effect 体逐字未改）。
import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import {
  bootstrapService,
  type BootstrapState,
} from "../services/net/bootstrap";
import { reportError } from "../services/net/error";
import { getRootNodeId, setRootNodeId, setRootNodeMap } from "../services/net/rootNode";
import { buildMatchInputFromPath, buildMessageWithViewContext, hasExplicitFileContext, indexManagedRoots, inferReadModeFromPlugin, managedDirAddErrorMessage, mapManagedRootsToEntries, normalizeUpdateState, shouldShowUpdateButton, toPluginInput, updateButtonLabel, updateSummaryText, useResponsive, waitForNextPaint } from "./appMisc";
import { basenameOfPath, buildDirectorySelectionKey, buildFileScrollKey, buildURLSearch, comparableManagedRootPath, dirnameOfPath, isDirectorySortMode, joinDisplayPath, normalizeCursor, normalizePath, parentDirsOfFile, parseFileLocation, parsePluginQuery, readURLState, relativeDisplayPathFromRoot, rootNodeKey } from "./appPath";
import { accountScopedKey, loadGitDiffSideBySide, loadLastRootId, loadLastRootNodeId, loadMobileEnterKeySends, loadPersistedFileScrollPositions, loadPersistedPluginQuery, loadSidebarsSwapped, loadTaskCreateWorktreePreference, persistFileScrollPositions, persistPluginQuery, removeLocalStorageByPrefix, saveTaskCreateWorktreePreference } from "./appStorage";
import type { Dispatch, SetStateAction, RefObject } from "react";
import {
  DEFAULT_DIRECTORY_SORT_MODE,
  type DirectorySortMode,
  type FileEntry,
} from "../services/prefs/directorySort";
import { APP_DOCUMENT_TITLE, AppProps, CHILD_SESSION_PAGE_SIZE, MULTI_PROJECT_SESSION_LIMIT, ManagedRootPayload, SESSION_PAGE_SIZE } from "./appMisc";
import { MainViewMode, URLState } from "./appPath";
import { useActionHandlers, type ActionHandlers } from "./useActionHandlers";
import { useI18n } from "../i18n";

export interface useAppInitParams {
  actionHandlersRef: RefObject<ActionHandlers>;
  bootstrapService: typeof bootstrapService;
  bootstrapState: BootstrapState;
  didInitRef: RefObject<boolean>;
  ensurePluginsLoaded: (rootId: string) => Promise<void>;
  handleSelectSessionRef: RefObject<((session: any, options?: { preserveTaskSelection?: boolean | undefined; preserveMainView?: boolean | undefined; } | undefined) => Promise<void>) | null>;
  indexManagedRoots: typeof indexManagedRoots;
  loadLastRootId: typeof loadLastRootId;
  loadLastRootNodeId: typeof loadLastRootNodeId;
  loadManagedRootPayloads: (opts?: { force?: boolean | undefined; } | undefined) => Promise<ManagedRootPayload[] | null>;
  loadMultiProjectSessionGroups: () => Promise<void>;
  loadSessionsForRoot: (rootID: string, options?: { beforeTime?: string | undefined; afterTime?: string | undefined; replace?: boolean | undefined; force?: boolean | undefined; } | undefined) => Promise<void>;
  managedRootByIdRef: RefObject<Record<string, ManagedRootPayload>>;
  managedRootByKeyRef: RefObject<Record<string, ManagedRootPayload>>;
  managedRootIdsRef: RefObject<Set<string>>;
  mapManagedRootsToEntries: typeof mapManagedRootsToEntries;
  multiProjectSessionsEnabled: true;
  readURLState: typeof readURLState;
  refreshTreeDir: (rootID: string, dirPath: string, syncMain: boolean) => Promise<void>;
  reportError: typeof reportError;
  selectRootNode: (rootId: string, nodeId?: string | undefined) => void;
  setCurrentRootId: Dispatch<SetStateAction<string | null>>;
  setManagedRootIds: Dispatch<SetStateAction<string[]>>;
  setPluginQuery: Dispatch<SetStateAction<Record<string, string>>>;
  setRootEntries: Dispatch<SetStateAction<FileEntry[]>>;
  setRootNodeMap: typeof setRootNodeMap;
  showBoundSessionOrRootDir: (rootID: string, options?: { pluginQuery?: Record<string, string> | undefined; closeLeftSidebar?: boolean | undefined; loadSessionsOnRestore?: boolean | undefined; suppressTreeExpand?: boolean | undefined; } | undefined) => Promise<void>;
  switchMainView: (mode: MainViewMode) => void;
  t: ReturnType<typeof useI18n>["t"];
  useEffect: typeof useEffect;
}

export function useAppInit(params: useAppInitParams) {
  const actionHandlersRef = params.actionHandlersRef;
  const bootstrapService = params.bootstrapService;
  const bootstrapState = params.bootstrapState;
  const didInitRef = params.didInitRef;
  const ensurePluginsLoaded = params.ensurePluginsLoaded;
  const handleSelectSessionRef = params.handleSelectSessionRef;
  const indexManagedRoots = params.indexManagedRoots;
  const loadLastRootId = params.loadLastRootId;
  const loadLastRootNodeId = params.loadLastRootNodeId;
  const loadManagedRootPayloads = params.loadManagedRootPayloads;
  const loadMultiProjectSessionGroups = params.loadMultiProjectSessionGroups;
  const loadSessionsForRoot = params.loadSessionsForRoot;
  const managedRootByIdRef = params.managedRootByIdRef;
  const managedRootByKeyRef = params.managedRootByKeyRef;
  const managedRootIdsRef = params.managedRootIdsRef;
  const mapManagedRootsToEntries = params.mapManagedRootsToEntries;
  const multiProjectSessionsEnabled = params.multiProjectSessionsEnabled;
  const readURLState = params.readURLState;
  const refreshTreeDir = params.refreshTreeDir;
  const reportError = params.reportError;
  const selectRootNode = params.selectRootNode;
  const setCurrentRootId = params.setCurrentRootId;
  const setManagedRootIds = params.setManagedRootIds;
  const setPluginQuery = params.setPluginQuery;
  const setRootEntries = params.setRootEntries;
  const setRootNodeMap = params.setRootNodeMap;
  const showBoundSessionOrRootDir = params.showBoundSessionOrRootDir;
  const switchMainView = params.switchMainView;
  const t = params.t;
  const useEffect = params.useEffect;

useEffect(() => {
    if (didInitRef.current) {
      return;
    }
    didInitRef.current = true;
    let cancelled = false;
    let settled = false;
    void (async () => {
      try {
        const bootstrap = await bootstrapService.start();
        if (bootstrap.phase !== "ready") {
          didInitRef.current = false;
          return;
        }
        const dirs = await loadManagedRootPayloads();
        if (!dirs) {
          return;
        }
        if (cancelled || !dirs.length) {
          return;
        }
        const nextDirs = dirs as ManagedRootPayload[];
        const ids = nextDirs.map((d) => d.id);
        const indexed = indexManagedRoots(nextDirs);
        managedRootByKeyRef.current = indexed.byKey;
        managedRootByIdRef.current = indexed.byId;
        setRootNodeMap(managedRootByIdRef.current as Record<string, any>);
        managedRootIdsRef.current = new Set(ids);
        setManagedRootIds(ids);
        setRootEntries(mapManagedRootsToEntries(nextDirs));
        const urlState = readURLState();
        const lastRoot = loadLastRootId();
        const lastNode = loadLastRootNodeId();
        const urlNode = String(urlState.node || "");
        const nodeHint = urlNode || lastNode || "";
        const preferredRoot =
          urlState.root && ids.includes(urlState.root)
            ? urlState.root
            : lastRoot && ids.includes(lastRoot)
              ? lastRoot
              : ids[0];
        const preferredDir =
          nextDirs.find(
            (d) => d.id === preferredRoot && (!nodeHint || (d as any)._nodeId === nodeHint),
          ) ||
          nextDirs.find((d) => d.id === preferredRoot);
        setCurrentRootId(preferredRoot);
        selectRootNode(
          preferredRoot,
          urlNode || (preferredDir as any)?._nodeId || undefined,
        );
        // 初始化竞态兜底：refs 就绪后补一次多项目会话加载，覆盖首屏早期空/回退 _nodeId 批次
        if (multiProjectSessionsEnabled) {
          void loadMultiProjectSessionGroups();
        }
        setPluginQuery(urlState.pluginQuery);
        if (urlState.session) {
          // 有明确的会话 id：直接进那个对话。抢在 handleSelectSession 之前切，
          // 否则它要等缓存/网络回来才切，中间会闪一下工作台。
          switchMainView("chat");
          if (cancelled) return;
          await handleSelectSessionRef.current?.(
            {
              key: urlState.session,
              session_key: urlState.session,
              root_id: preferredRoot,
            },
            // 已经切到 chat 了，别让 handleSelectSession 再按 URL 的 view 改回去
            // （深链 ?session=k&view=board 也一律进对话：会话 id 是明确的落点）。
            { preserveMainView: true },
          );
        } else if (urlState.file) {
          await ensurePluginsLoaded(preferredRoot);
          if (cancelled) return;
          actionHandlersRef.current.open({
            path: urlState.file,
            root: preferredRoot,
            cursor: urlState.cursor,
            preservePluginQuery: true,
            preserveMainView: !!urlState.view && urlState.view !== "files",
          });
        } else {
          if (cancelled) return;
          await showBoundSessionOrRootDir(preferredRoot, {
            pluginQuery: urlState.pluginQuery,
            loadSessionsOnRestore: true,
          });
        }
      } catch (err) {
        if (cancelled) {
          return;
        }
        reportError(
          "app.init_failed",
          String((err as Error)?.message || err || t("app.initFailed")),
        );
      } finally {
        settled = true;
      }
    })();
    return () => {
      cancelled = true;
      if (!settled) {
        didInitRef.current = false;
      }
    };
  }, [
    ensurePluginsLoaded,
    loadManagedRootPayloads,
    loadMultiProjectSessionGroups,
    loadSessionsForRoot,
    refreshTreeDir,
    showBoundSessionOrRootDir,
    bootstrapState.phase,
  ])
}
