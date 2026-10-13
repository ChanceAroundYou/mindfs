import { useRef } from "react";
import { PluginManager } from "../plugins/manager";
import type { Session } from "../services/session";
import type { FilePayload } from "../services/file";
import type { ManagedRootPayload } from "./appMisc";
import type { PendingSend, SessionItem, SessionQueueItem, ViewerSelection } from "./appSession";
import { loadPersistedFileScrollPositions } from "./appStorage";
import { useSessionCache } from "./useSessionCache";

/**
 * App 状态层使用的命令式 ref 集合。
 * 保持原声明顺序，因为其中的会话缓存也是一个 hook。
 */
export function useAppStateRefs() {
  const pluginManagerRef = useRef<PluginManager>(new PluginManager());
  const managedRootIdsRef = useRef<Set<string>>(new Set());
  const expandedRef = useRef<string[]>([]);
  const selectedDirRef = useRef<string | null>(null);
  const fileRef = useRef<FilePayload | null>(null);
  const selectedSessionRef = useRef<SessionItem | null>(null);
  const lastMainSessionSnapshotRef = useRef<Session | null>(null);
  const sessionSearchTargetCounterRef = useRef(0);
  const currentSessionRef = useRef<SessionItem | null>(null);
  const interactionModeRef = useRef<"main" | "drawer">("main");
  const pendingDraftRef = useRef<PendingSend | null>(null);
  const pendingBySessionRef = useRef<Record<string, PendingSend>>({});
  const pendingRequestRef = useRef<Record<string, PendingSend>>({});
  const queuedMessagesBySessionRef = useRef<Record<string, SessionQueueItem[]>>({});
  const queueFrozenBySessionRef = useRef<Record<string, boolean>>({});
  const optimisticDequeuedIdsRef = useRef<Record<string, Set<string>>>({});
  const cancelRequestedBySessionRef = useRef<Record<string, boolean>>({});
  // 会话缓存（快照表 + 加载状态/失败记录/LRU 淘汰）搬进 app/useSessionCache.ts：
  // 策略集中在一处，App.tsx 只留读写点。refs 语义与变量名不变，调用点零改动。
  const {
    sessionCacheRef,
    loadedSessionRef,
    loadingSessionRef,
    sessionLoadFailureRef,
    sessionCacheAccessRef,
    staleSessionKeysRef,
    enforceSessionCacheBound,
  } = useSessionCache();
  const invalidTreeCacheKeysRef = useRef<Set<string>>(new Set());
  // 上一轮跨节点抓取里失败的节点。refreshManagedRoots 消费它：提示用户 + 给出重试入口。
  // 跨节点失败原本被静默吞成 []，整个节点凭空缺席且无人重拉（实测可长时间不恢复）。
  const nodeLoadFailuresRef = useRef<Array<{ id: string; name: string }>>([]);
  const notifiedNodeFailuresRef = useRef<Set<string>>(new Set());
  const notifyNodeLoadFailedRef = useRef<(node: { id: string; name: string }) => void>(() => {});
  // 目录树刷新并发守卫：按缓存键记录最后发起的请求序号，旧响应后到时直接丢弃
  const treeFetchSeqRef = useRef<Record<string, number>>({});
  const boundSessionByRootRef = useRef<Record<string, string | null>>({});
  const suppressedAutoBindSessionByRootRef = useRef<Record<string, string | null>>({});
  const drawerSessionByRootRef = useRef<Record<string, SessionItem | null>>({});
  const selectedSessionByRootRef = useRef<Record<string, string | null>>({});
  const drawerOpenByRootRef = useRef<Record<string, boolean>>({});
  const drawerScrollRef = useRef<HTMLDivElement | null>(null);
  const fileCursorRef = useRef<number>(0);
  const fileScrollPositionsRef = useRef<Record<string, number>>(loadPersistedFileScrollPositions());
  const pluginContentRef = useRef<HTMLDivElement | null>(null);
  const lastPluginChapterRef = useRef<string>("");
  const viewerSelectionRef = useRef<ViewerSelection | null>(null);
  const lastViewerSelectionRef = useRef<ViewerSelection | null>(null);
  const dismissedSelectionFileRef = useRef<string | null>(null);
  const lastPluginResetFileKeyRef = useRef<string>("");
  const pluginBypassRef = useRef<boolean>(false);
  const fileOpenRequestRef = useRef(0);
  const fullUpgradeAttemptRef = useRef("");
  const pluginsLoadedByRootRef = useRef<Record<string, boolean>>({});
  const pluginsLoadingByRootRef = useRef<Record<string, Promise<void>>>({});
  const pluginsTrustPendingByRootRef = useRef<Record<string, boolean>>({});
  const didInitRef = useRef(false);
  const managedRootsRequestRef = useRef<Promise<ManagedRootPayload[] | null> | null>(null);
  const handleSelectSessionRef = useRef<
    | ((session: any, options?: { preserveTaskSelection?: boolean; preserveMainView?: boolean }) => Promise<void>)
    | null
  >(null);

  return {
    pluginManagerRef, managedRootIdsRef, expandedRef, selectedDirRef, fileRef, selectedSessionRef,
    lastMainSessionSnapshotRef, sessionSearchTargetCounterRef, currentSessionRef, interactionModeRef,
    pendingDraftRef, pendingBySessionRef, pendingRequestRef, queuedMessagesBySessionRef,
    queueFrozenBySessionRef, optimisticDequeuedIdsRef, cancelRequestedBySessionRef,
    sessionCacheRef, loadedSessionRef, loadingSessionRef, sessionLoadFailureRef,
    sessionCacheAccessRef, staleSessionKeysRef, enforceSessionCacheBound,
    invalidTreeCacheKeysRef, nodeLoadFailuresRef, notifiedNodeFailuresRef, notifyNodeLoadFailedRef,
    treeFetchSeqRef, boundSessionByRootRef, suppressedAutoBindSessionByRootRef,
    drawerSessionByRootRef, selectedSessionByRootRef, drawerOpenByRootRef, drawerScrollRef,
    fileCursorRef, fileScrollPositionsRef, pluginContentRef, lastPluginChapterRef,
    viewerSelectionRef, lastViewerSelectionRef, dismissedSelectionFileRef, lastPluginResetFileKeyRef,
    pluginBypassRef, fileOpenRequestRef, fullUpgradeAttemptRef, pluginsLoadedByRootRef,
    pluginsLoadingByRootRef, pluginsTrustPendingByRootRef, didInitRef, managedRootsRequestRef,
    handleSelectSessionRef,
  };
}
