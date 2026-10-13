// 从 App.tsx 抽出的 useEffect（机械搬移，effect 体逐字未改）。
import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { closeTopBackLayer, consumeViewHistoryEntry, hasBackLayer, installBackNavigation, pushViewHistoryEntry, useBackLayer } from "../hooks/useBackNavigation";
import { basenameOfPath, buildDirectorySelectionKey, buildFileScrollKey, buildURLSearch, comparableManagedRootPath, dirnameOfPath, isDirectorySortMode, joinDisplayPath, normalizeCursor, normalizePath, parentDirsOfFile, parseFileLocation, parsePluginQuery, readURLState, relativeDisplayPathFromRoot, rootNodeKey } from "./appPath";
import type { Dispatch, SetStateAction, RefObject } from "react";
import {
  fetchFile,
  fetchEditableFile,
  saveTextFile,
  clearFileCacheForRoot,
  clearFileMemoryCacheForView,
  getCachedFile,
  invalidateFileCache,
  type FilePayload,
} from "../services/file";
import { MainViewMode, URLState } from "./appPath";
import { AttachedFileContext, Exchange, GitFileStat, MultiProjectSessionGroup, PendingSend, RelatedFileClickTarget, SessionItem, SessionMode, SessionQueueItem, SlashCommandResult, ViewerSelection, WSStatus } from "./appSession";
import { useActionHandlers, type ActionHandlers } from "./useActionHandlers";

export interface usePopStateSyncParams {
  actionHandlers: ActionHandlers;
  closeTopBackLayer: typeof closeTopBackLayer;
  consumeViewHistoryEntry: typeof consumeViewHistoryEntry;
  currentRootIdRef: RefObject<string | null>;
  fileCursorRef: RefObject<number>;
  fileRef: RefObject<FilePayload | null>;
  handleSelectSessionRef: RefObject<((session: any, options?: { preserveTaskSelection?: boolean | undefined; preserveMainView?: boolean | undefined; } | undefined) => Promise<void>) | null>;
  hasBackLayer: typeof hasBackLayer;
  lastNonChatViewRef: RefObject<MainViewMode>;
  loadSessionsForRoot: (rootID: string, options?: { beforeTime?: string | undefined; afterTime?: string | undefined; replace?: boolean | undefined; force?: boolean | undefined; } | undefined) => Promise<void>;
  mainViewRef: RefObject<MainViewMode>;
  readURLState: typeof readURLState;
  refreshTreeDir: (rootID: string, dirPath: string, syncMain: boolean) => Promise<void>;
  replaceURLState: (next: URLState) => void;
  selectRootNode: (rootId: string, nodeId?: string | undefined) => void;
  selectedSessionRef: RefObject<SessionItem | null>;
  setCurrentRootId: Dispatch<SetStateAction<string | null>>;
  setMainView: Dispatch<SetStateAction<MainViewMode>>;
  setPluginQuery: Dispatch<SetStateAction<Record<string, string>>>;
  showBoundSessionOrRootDir: (rootID: string, options?: { pluginQuery?: Record<string, string> | undefined; closeLeftSidebar?: boolean | undefined; loadSessionsOnRestore?: boolean | undefined; suppressTreeExpand?: boolean | undefined; } | undefined) => Promise<void>;
  switchMainView: (mode: MainViewMode) => void;
  useEffect: typeof useEffect;
}

export function usePopStateSync(params: usePopStateSyncParams) {
  const actionHandlers = params.actionHandlers;
  const closeTopBackLayer = params.closeTopBackLayer;
  const consumeViewHistoryEntry = params.consumeViewHistoryEntry;
  const currentRootIdRef = params.currentRootIdRef;
  const fileCursorRef = params.fileCursorRef;
  const fileRef = params.fileRef;
  const handleSelectSessionRef = params.handleSelectSessionRef;
  const hasBackLayer = params.hasBackLayer;
  const lastNonChatViewRef = params.lastNonChatViewRef;
  const loadSessionsForRoot = params.loadSessionsForRoot;
  const mainViewRef = params.mainViewRef;
  const readURLState = params.readURLState;
  const refreshTreeDir = params.refreshTreeDir;
  const replaceURLState = params.replaceURLState;
  const selectRootNode = params.selectRootNode;
  const selectedSessionRef = params.selectedSessionRef;
  const setCurrentRootId = params.setCurrentRootId;
  const setMainView = params.setMainView;
  const setPluginQuery = params.setPluginQuery;
  const showBoundSessionOrRootDir = params.showBoundSessionOrRootDir;
  const switchMainView = params.switchMainView;
  const useEffect = params.useEffect;

  useEffect(() => {
      function handlePopState() {
        const state = readURLState();
        if (state.root) {
          setCurrentRootId(state.root);
          selectRootNode(state.root, state.node || undefined);
        }
        setPluginQuery(state.pluginQuery);
        if (state.view) {
          // 后退/前进时按钮高亮与面板都从 URL 的 view 恢复，保持同源
          switchMainView(state.view);
        }
        if (!state.root) {
          return;
        }
        if (state.session) {
          const currentSessionKey =
            selectedSessionRef.current?.key ||
            selectedSessionRef.current?.session_key ||
            "";
          const currentSessionRoot =
            (selectedSessionRef.current?.root_id as string | undefined) ||
            currentRootIdRef.current ||
            "";
          if (
            state.session !== currentSessionKey ||
            state.root !== currentSessionRoot
          ) {
            void handleSelectSessionRef.current?.(
              {
                key: state.session,
                session_key: state.session,
                root_id: state.root,
              },
              { preserveMainView: !!state.view && state.view !== "chat" },
            );
          }
          return;
        }
        if (state.file) {
          const currentPath = fileRef.current?.path || "";
          const currentRoot = currentRootIdRef.current || "";
          const currentCursor = fileCursorRef.current;
          if (
            state.file !== currentPath ||
            state.root !== currentRoot ||
            state.cursor !== currentCursor
          ) {
            actionHandlers.open({
              path: state.file,
              root: state.root,
              cursor: state.cursor,
              preservePluginQuery: true,
              preserveMainView: !!state.view && state.view !== "files",
            });
          }
          return;
        }
        void (async () => {
          await showBoundSessionOrRootDir(state.root, {
            pluginQuery: state.pluginQuery,
            loadSessionsOnRestore: true,
          });
        })();
      }
  
      const onPopState = (event: PopStateEvent) => {
        // 覆盖层开着的时候，浏览器后退/iOS 侧滑应该只关那一层，而不是连视图一起换掉。
        // 刚被弹掉的那一条立刻 push 回去，否则历史被吃掉一次，
        // 下一次后退就直接退到站外（用户看到的是「按两次返回就出去了」）。
        if (hasBackLayer()) {
          window.history.pushState(event.state, "", window.location.href);
          closeTopBackLayer();
          return;
        }
        // 视图切换留下的那条记录：它带的是「切之前」那个视图，直接退回去。
        // 不走 handlePopState —— 那套按 URL 恢复，而 pushViewHistoryEntry 存的
        // 是切换瞬间的 URL，view 字段可能还没写进去，判据只能是 state 里的视图。
        // 退到哪个视图后要 replaceState 把 URL 对齐（按钮高亮与实际视图同源），
        // 只换 view、其余 root/file/session 原样带回，否则会把当前项目/文件抹掉。
        const fromView = (event.state as { mindfsView?: MainViewMode } | null)?.mindfsView;
        if (fromView) {
          consumeViewHistoryEntry();
          mainViewRef.current = fromView;
          setMainView(fromView);
          if (fromView !== "chat") {
            lastNonChatViewRef.current = fromView;
          }
          const now = readURLState();
          replaceURLState({ ...now, view: fromView });
          return;
        }
        handlePopState();
      };
      window.addEventListener("popstate", onPopState);
      return () => window.removeEventListener("popstate", onPopState);
    }, [actionHandlers, loadSessionsForRoot, refreshTreeDir, showBoundSessionOrRootDir])
}
