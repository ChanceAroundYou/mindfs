import { useEffect } from "react";
import { protectedAPIReady } from "../services/net/api";
import { refreshPinsFromServer } from "../services/pins";
import { fetchAgents } from "../services/agents";
import { accountScopedKey, DIRECTORY_SORT_OVERRIDES_STORAGE_KEY, LAST_ROOT_NODE_STORAGE_KEY, LAST_ROOT_STORAGE_KEY, TREE_SORT_STORAGE_KEY } from "./appStorage";
import type { MultiProjectSessionGroup, SessionItem, ViewerSelection } from "./appSession";
import type { DirectorySortMode, FileEntry } from "../services/prefs/directorySort";
import type { FilePayload } from "../services/file";
import type { AgentStatus } from "../services/agents";

/**
 * 把最新 state 镜像进 ref，外加同一段里的几件环境副作用。
 */
export type UseAppRefSyncParams = {
  currentRootIdRef: React.RefObject<string | null>;
  currentRootId: string | null;
  currentRootNodeIdRef: React.RefObject<string | null>;
  currentRootNodeId: string | null;
  setAvailableAgents: React.Dispatch<React.SetStateAction<AgentStatus[]>>;
  agentsVersion: number;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  expandedRef: React.RefObject<string[]>;
  expanded: string[];
  selectedDirRef: React.RefObject<string | null>;
  selectedDir: string | null;
  entriesByPathRef: React.RefObject<Record<string, FileEntry[]>>;
  entriesByPath: Record<string, FileEntry[]>;
  fileRef: React.RefObject<FilePayload | null>;
  file: FilePayload | null;
  viewerSelectionRef: React.RefObject<ViewerSelection | null>;
  viewerSelection: ViewerSelection | null;
  pluginQueryRef: React.RefObject<Record<string, string>>;
  pluginQuery: Record<string, string>;
  lastPluginResetFileKeyRef: React.RefObject<string>;
  pluginBypassRef: React.RefObject<boolean>;
  setPluginBypass: React.Dispatch<React.SetStateAction<boolean>>;
  pluginBypass: boolean;
  selectedSessionRef: React.RefObject<SessionItem | null>;
  selectedSession: SessionItem | null;
  activeBoundSessionKey: string | null;
  setActiveBoundSessionKey: React.Dispatch<React.SetStateAction<string | null>>;
  interactionMode: "main" | "drawer";
  setPendingPlanMode: React.Dispatch<React.SetStateAction<boolean>>;
  sessionsRef: React.RefObject<SessionItem[]>;
  sessions: SessionItem[];
  multiProjectSessionGroupsRef: React.RefObject<MultiProjectSessionGroup[]>;
  multiProjectSessionGroups: MultiProjectSessionGroup[];
  multiProjectPendingRef: React.RefObject<Record<string, boolean>>;
  multiProjectPendingByKey: Record<string, boolean>;
  multiProjectAwaitingRef: React.RefObject<Record<string, boolean>>;
  multiProjectAwaitingByKey: Record<string, boolean>;
  currentSessionRef: React.RefObject<SessionItem | null>;
  currentSession: SessionItem | null;
  interactionModeRef: React.RefObject<"main" | "drawer">;
  treeSortMode: DirectorySortMode;
  directorySortOverrides: Record<string, DirectorySortMode>;
  scopedRootKey: (rootId: string) => string;
  boundSessionByRootRef: React.RefObject<Record<string, string | null>>;
  setCurrentSession: React.Dispatch<React.SetStateAction<SessionItem | null>>;
  drawerSessionByRootRef: React.RefObject<Record<string, SessionItem | null>>;
  setIsDrawerOpen: React.Dispatch<React.SetStateAction<boolean>>;
  drawerOpenByRootRef: React.RefObject<Record<string, boolean>>;
};

/**
 * 这 24 条 effect 是一段**连续**的声明，必须整段搬 —— effect 之间如果在别处
 * 插进另一个 effect，相对顺序就变了（React 按声明顺序跑）。
 *
 * 前十几条是同一件事：`xxxRef.current = xxx`。做法本身不优雅，但异步回调、
 * WS 处理器、事件监听里读到的只能是 ref（闭包里的 state 永远是旧的），
 * 所以每次 render 都把最新值刷进去。**别把这些合并或省掉** —— 少一条就会有
 * 某个回调读到上一帧的值。
 *
 * 夹在中间的四条是同一段里的真副作用：加载 agents 列表、监听 window、
 * 按 root 记 last-root/last-node、以及排序偏好的持久化。它们留在这里不是因为
 * 跟镜像同类，而是因为**挪出去就会改变 effect 的执行次序**。
 */
export function useAppRefSync(params: UseAppRefSyncParams) {
  const { currentRootIdRef, currentRootId, currentRootNodeIdRef, currentRootNodeId, setAvailableAgents, agentsVersion, getNodeIdForRoot, expandedRef, expanded, selectedDirRef, selectedDir, entriesByPathRef, entriesByPath, fileRef, file, viewerSelectionRef, viewerSelection, pluginQueryRef, pluginQuery, lastPluginResetFileKeyRef, pluginBypassRef, setPluginBypass, pluginBypass, selectedSessionRef, selectedSession, activeBoundSessionKey, setActiveBoundSessionKey, interactionMode, setPendingPlanMode, sessionsRef, sessions, multiProjectSessionGroupsRef, multiProjectSessionGroups, multiProjectPendingRef, multiProjectPendingByKey, multiProjectAwaitingRef, multiProjectAwaitingByKey, currentSessionRef, currentSession, interactionModeRef, treeSortMode, directorySortOverrides, scopedRootKey, boundSessionByRootRef, setCurrentSession, drawerSessionByRootRef, setIsDrawerOpen, drawerOpenByRootRef } = params;

  useEffect(() => {
    currentRootIdRef.current = currentRootId;
  }, [currentRootId]);

  useEffect(() => {
    currentRootNodeIdRef.current = currentRootNodeId;
  }, [currentRootNodeId]);

  useEffect(() => {
    let cancelled = false;
    fetchAgents(true, getNodeIdForRoot(currentRootId || "") as any)
      .then((items) => {
        if (cancelled) return;
        setAvailableAgents(items);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [agentsVersion, currentRootId]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    if (currentRootId) {
      window.localStorage.setItem(accountScopedKey(LAST_ROOT_STORAGE_KEY), currentRootId);
      window.localStorage.setItem(accountScopedKey(LAST_ROOT_NODE_STORAGE_KEY), currentRootNodeId || "");
    }
  }, [currentRootId, currentRootNodeId]);

  useEffect(() => {
    expandedRef.current = expanded;
  }, [expanded]);

  useEffect(() => {
    selectedDirRef.current = selectedDir;
  }, [selectedDir]);

  useEffect(() => {
    entriesByPathRef.current = entriesByPath;
  }, [entriesByPath]);

  useEffect(() => {
    fileRef.current = file;
  }, [file]);

  useEffect(() => {
    viewerSelectionRef.current = viewerSelection;
  }, [viewerSelection]);

  useEffect(() => {
    pluginQueryRef.current = pluginQuery;
  }, [pluginQuery]);

  useEffect(() => {
    if (!file?.path || !currentRootId) return;
    const nextKey = `${currentRootId}:${file.path}`;
    if (lastPluginResetFileKeyRef.current !== nextKey) {
      pluginBypassRef.current = false;
      setPluginBypass(false);
      lastPluginResetFileKeyRef.current = nextKey;
    }
  }, [file?.path, currentRootId]);

  useEffect(() => {
    pluginBypassRef.current = pluginBypass;
  }, [pluginBypass]);

  useEffect(() => {
    selectedSessionRef.current = selectedSession;
  }, [selectedSession]);

  useEffect(() => {
    if (selectedSession || activeBoundSessionKey || interactionMode !== "main") {
      setPendingPlanMode(false);
    }
  }, [selectedSession, activeBoundSessionKey, interactionMode]);

  useEffect(() => {
    sessionsRef.current = sessions;
  }, [sessions]);

  useEffect(() => {
    multiProjectSessionGroupsRef.current = multiProjectSessionGroups;
  }, [multiProjectSessionGroups]);

  useEffect(() => {
    multiProjectPendingRef.current = multiProjectPendingByKey;
  }, [multiProjectPendingByKey]);

  useEffect(() => {
    multiProjectAwaitingRef.current = multiProjectAwaitingByKey;
  }, [multiProjectAwaitingByKey]);

  useEffect(() => {
    setPendingPlanMode(false);
  }, [currentRootId]);

  useEffect(() => {
    if (!currentRootId || !protectedAPIReady()) return;
    // 拉不到就保持当前那份：置顶是装饰性排序，一个装饰性请求失败不该把
    // 正在看的列表闪成「全不置顶」。writePins 内部按指纹判断要不要通知订
    // 阅者，所以「服务端没变化」不会引起任何重排。
    void refreshPinsFromServer().catch(() => {});
  }, [currentRootId]);

  useEffect(() => {
    currentSessionRef.current = currentSession;
  }, [currentSession]);

  useEffect(() => {
    interactionModeRef.current = interactionMode;
  }, [interactionMode]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    window.localStorage.setItem(TREE_SORT_STORAGE_KEY, treeSortMode);
  }, [treeSortMode]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    window.localStorage.setItem(
      DIRECTORY_SORT_OVERRIDES_STORAGE_KEY,
      JSON.stringify(directorySortOverrides),
    );
  }, [directorySortOverrides]);

  useEffect(() => {
    const rootID = currentRootId;
    if (!rootID) return;
    setActiveBoundSessionKey(boundSessionByRootRef.current[scopedRootKey(rootID)] || null);
    setCurrentSession(drawerSessionByRootRef.current[scopedRootKey(rootID)] || null);
    setIsDrawerOpen(!!drawerOpenByRootRef.current[scopedRootKey(rootID)]);
  }, [currentRootId]);

}
