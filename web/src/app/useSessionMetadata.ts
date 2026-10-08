/**
 * 会话元数据写回 —— A7 簇（~224 行）。
 *
 * 覆盖：关联文件 / 关联 worktree / agent·模型·effort·plan 的按会话写回
 * （缓存 + lastMain 快照 + 选中项 + 抽屉四处同步），以及输入会话解析、
 * plan mode 切换、会话头从列表项同步的 effect。
 *
 * 做法：hook，参数面 UseSessionMetadataParams 即完整契约；函数体逐字搬入
 * （保持源码正则断言可命中，配合 source-map 注册）。
 */
import { useCallback, useEffect, type MutableRefObject, type Dispatch, type SetStateAction } from "react";
import { useI18n } from "../i18n";
import { SessionItem } from "./appSession";
import { useSessionCache } from "./useSessionCache";
import { resolveInputSession } from "../services/inputSession";
import { reportError } from "../services/net/error";
import { type RelatedFile, type RelatedWorktree, type Session, sessionService } from "../services/session";

export interface UseSessionMetadataParams {
  bumpCacheVersion: () => any;
  currentRootIdRef: MutableRefObject<string | null>;
  drawerOpenByRootRef: MutableRefObject<Record<string, boolean>>;
  drawerSessionByRootRef: MutableRefObject<Record<string, SessionItem | null>>;
  lastMainSessionSnapshotRef: MutableRefObject<Session | null>;
  rootSessionKey: (rootId: string, sessionKey: string) => any;
  scopedRootKey: (rootId: string) => string;
  selectedSessionRef: MutableRefObject<SessionItem | null>;
  sessionCacheRef: ReturnType<typeof useSessionCache>["sessionCacheRef"];
  sessions: SessionItem[];
  setDrawerSessionForRoot: (rootID: string | null | undefined, session: Session | SessionItem | null) => any;
  setPendingPlanMode: Dispatch<SetStateAction<boolean>>;
  setSelectedSession: Dispatch<SetStateAction<SessionItem | null>>;
  syncSessionHeaderFromListItem: (rootId: string, item: SessionItem) => any;
  t: ReturnType<typeof useI18n>["t"];
}

export function useSessionMetadata(params: UseSessionMetadataParams) {
  const {
    bumpCacheVersion,
    currentRootIdRef,
    drawerOpenByRootRef,
    drawerSessionByRootRef,
    lastMainSessionSnapshotRef,
    rootSessionKey,
    scopedRootKey,
    selectedSessionRef,
    sessionCacheRef,
    sessions,
    setDrawerSessionForRoot,
    setPendingPlanMode,
    setSelectedSession,
    syncSessionHeaderFromListItem,
    t,
  } = params;

  const updateSessionRelatedFilesForKey = useCallback(
    (rootID: string, sessionKey: string, relatedFiles: RelatedFile[]) => {
      if (!rootID || !sessionKey) return;
      const cacheKey = rootSessionKey(rootID, sessionKey);
      const cached = sessionCacheRef.current[cacheKey];
      const nextRelatedFiles = Array.isArray(relatedFiles)
        ? [...relatedFiles]
        : [];
      if (cached) {
        sessionCacheRef.current[cacheKey] = {
          ...(cached as any),
          related_files: nextRelatedFiles,
        } as Session;
      }
      const lastMain = lastMainSessionSnapshotRef.current;
      const lastMainKey = lastMain?.key || lastMain?.session_key;
      const lastMainRoot =
        (lastMain?.root_id as string | undefined) || currentRootIdRef.current;
      if (lastMain && lastMainKey === sessionKey && lastMainRoot === rootID) {
        lastMainSessionSnapshotRef.current = {
          ...(lastMain as any),
          related_files: nextRelatedFiles,
        } as Session;
      }
      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        const prevRoot =
          (prev?.root_id as string | undefined) || currentRootIdRef.current;
        if (!prev || prevKey !== sessionKey || prevRoot !== rootID) return prev;
        return {
          ...(prev as any),
          related_files: nextRelatedFiles,
        } as SessionItem;
      });
      const current = drawerSessionByRootRef.current[scopedRootKey(rootID)];
      if (current && current.key === sessionKey) {
        setDrawerSessionForRoot(rootID, {
          ...(current as any),
          related_files: nextRelatedFiles,
        } as Session);
      }
      bumpCacheVersion();
    },
    [rootSessionKey, setDrawerSessionForRoot, bumpCacheVersion],
  );

  const updateSessionRelatedWorktreeForKey = useCallback(
    (rootID: string, sessionKey: string, relatedWorktree: RelatedWorktree | null | undefined) => {
      if (!rootID || !sessionKey || !relatedWorktree) return;
      const cacheKey = rootSessionKey(rootID, sessionKey);
      const cached = sessionCacheRef.current[cacheKey];
      if (cached) {
        sessionCacheRef.current[cacheKey] = {
          ...(cached as any),
          related_worktree: relatedWorktree,
        } as Session;
      }

      const lastMain = lastMainSessionSnapshotRef.current;
      const lastMainKey = lastMain?.key || lastMain?.session_key;
      const lastMainRoot =
        (lastMain?.root_id as string | undefined) || currentRootIdRef.current;
      if (lastMain && lastMainKey === sessionKey && lastMainRoot === rootID) {
        lastMainSessionSnapshotRef.current = {
          ...(lastMain as any),
          related_worktree: relatedWorktree,
        } as Session;
      }

      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        const prevRoot =
          (prev?.root_id as string | undefined) || currentRootIdRef.current;
        if (!prev || prevKey !== sessionKey || prevRoot !== rootID) return prev;
        return {
          ...(prev as any),
          related_worktree: relatedWorktree,
        } as SessionItem;
      });

      const current = drawerSessionByRootRef.current[scopedRootKey(rootID)];
      if (current && current.key === sessionKey) {
        setDrawerSessionForRoot(rootID, {
          ...(current as any),
          related_worktree: relatedWorktree,
        } as Session);
      }
      bumpCacheVersion();
    },
    [rootSessionKey, setDrawerSessionForRoot, bumpCacheVersion],
  );

  const updateSessionAgentForKey = useCallback(
    (
      rootID: string,
      sessionKey: string,
      agent: string,
      model?: string,
      agentMode?: string,
      effort?: string,
      fastService?: "" | "on" | "off",
      planMode?: boolean,
      shell?: string,
    ) => {
      if (!rootID || !sessionKey || !agent) return;
      const hasPlanMode = typeof planMode === "boolean";
      const cacheKey = rootSessionKey(rootID, sessionKey);
      const cached = sessionCacheRef.current[cacheKey];
      if (cached) {
        sessionCacheRef.current[cacheKey] = {
          ...(cached as any),
          agent,
          model: model || "",
          mode: agentMode || "",
          effort: effort || "",
          fast_service: fastService || "",
          ...(hasPlanMode ? { plan_mode: planMode } : {}),
          updated_at: new Date().toISOString(),
        } as Session;
      }
      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        const prevRoot =
          (prev?.root_id as string | undefined) || currentRootIdRef.current;
        if (!prev || prevKey !== sessionKey || prevRoot !== rootID) return prev;
        return {
          ...(prev as any),
          agent,
          model: model || "",
          mode: agentMode || "",
          effort: effort || "",
          fast_service: fastService || "",
          ...(hasPlanMode ? { plan_mode: planMode } : {}),
        } as SessionItem;
      });
      const current = drawerSessionByRootRef.current[scopedRootKey(rootID)];
      if (
        current &&
        current.key === sessionKey &&
        (current.agent !== agent ||
          (current as any).model !== (model || "") ||
          (current as any).mode !== (agentMode || "") ||
          (current as any).effort !== (effort || "") ||
          ((current as any).fast_service || "") !== (fastService || "") ||
          (hasPlanMode && !!(current as any).plan_mode !== planMode))
      ) {
        setDrawerSessionForRoot(rootID, {
          ...(current as any),
          agent,
          model: model || "",
          mode: agentMode || "",
          effort: effort || "",
          fast_service: fastService || "",
          ...(hasPlanMode ? { plan_mode: planMode } : {}),
        } as Session);
      }
      bumpCacheVersion();
    },
    [rootSessionKey, setDrawerSessionForRoot, bumpCacheVersion],
  );

  useEffect(() => {
    const rootID = currentRootIdRef.current;
    if (!rootID || sessions.length === 0) return;
    for (const item of sessions) {
      syncSessionHeaderFromListItem(rootID, item);
    }
  }, [sessions, syncSessionHeaderFromListItem]);

  const getInputSession = useCallback(() => {
    const root = currentRootIdRef.current;
    return resolveInputSession(
      root,
      !!drawerOpenByRootRef.current[root || ""],
      selectedSessionRef.current,
      drawerSessionByRootRef.current[root || ""] || null,
    );
  }, []);

  const handleSetPlanMode = useCallback(
    async (enabled: boolean, targetSessionKey?: string, targetRootId?: string) => {
      const activeRoot = targetRootId || currentRootIdRef.current;
      const session = getInputSession();
      const sessionKey = targetSessionKey || session?.key || (session as any)?.session_key;
      if (!activeRoot) {
        reportError("session.sync_failed", t("session.planModeSelectFirst"));
        return;
      }
      if (!sessionKey || String(sessionKey).startsWith("pending-")) {
        setPendingPlanMode(enabled);
        return;
      }
      const now = new Date().toISOString();
      const cacheKey = rootSessionKey(activeRoot, sessionKey);
      const cached = sessionCacheRef.current[cacheKey];
      if (cached) {
        sessionCacheRef.current[cacheKey] = {
          ...(cached as any),
          plan_mode: enabled,
          updated_at: now,
        } as Session;
      }
      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        const prevRoot = (prev?.root_id as string | undefined) || activeRoot;
        if (!prev || prevKey !== sessionKey || prevRoot !== activeRoot) return prev;
        return { ...(prev as any), plan_mode: enabled, updated_at: now } as SessionItem;
      });
      const drawer = drawerSessionByRootRef.current[scopedRootKey(activeRoot)];
      if (drawer?.key === sessionKey) {
        setDrawerSessionForRoot(activeRoot, {
          ...(drawer as any),
          plan_mode: enabled,
          updated_at: now,
        } as Session);
      }
      bumpCacheVersion();
      const sent = await sessionService.setPlanMode(activeRoot, sessionKey, enabled);
      if (!sent) {
        reportError("network.disconnected", t("session.planModeSwitchFailedNotReady"));
      }
    },
    [getInputSession, rootSessionKey, setDrawerSessionForRoot, bumpCacheVersion, t],
  );

  return {
    updateSessionRelatedFilesForKey,
    updateSessionRelatedWorktreeForKey,
    updateSessionAgentForKey,
    getInputSession,
    handleSetPlanMode,
  };
}
