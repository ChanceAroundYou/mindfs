/**
 * 会话关联文件与文件树指示点 —— A8 簇后半（~123 行）。
 *
 * 覆盖：点关联文件跳 diff（选中会话 / 抽屉两条路径）、抽屉会话快照、
 * 文件树根节点上的「已绑定 / 有回复」指示点、移除会话关联文件。
 *
 * 做法：hook，参数面 UseSessionRelatedFilesParams 即完整契约；函数体逐字搬入
 * （保持源码正则断言可命中，配合 source-map 注册）。
 */
import { useCallback, useMemo, type MutableRefObject, type Dispatch, type SetStateAction } from "react";
import { RelatedFileClickTarget, SessionItem } from "./appSession";
import { ManagedRootPayload } from "./appMisc";
import { useSessionMetadata } from "./useSessionMetadata";
import { useSessionPending } from "./useSessionPending";
import { useRelatedFileDiff } from "./useRelatedFileDiff";
import { scopeKey, scopeSessionKey } from "../shared/scope";
import { sessionService, setCachedSessionRelatedFiles, type RelatedFile, type Session } from "../services/session";
import type { ProjectTreeTab } from "../components/file/FileTree";

export interface UseSessionRelatedFilesParams {
  activeBoundSessionKey: string | null;
  boundSessionByRootRef: MutableRefObject<Record<string, string | null>>;
  cacheVersion: number;
  currentRootId: string | null;
  currentRootIdRef: MutableRefObject<string | null>;
  currentSession: SessionItem | null;
  drawerSessionByRootRef: MutableRefObject<Record<string, SessionItem | null>>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  getSessionSnapshot: ReturnType<typeof useSessionPending>["getSessionSnapshot"];
  managedRootByKeyRef: MutableRefObject<Record<string, ManagedRootPayload>>;
  managedRootIds: string[];
  multiProjectPendingByKey: Record<string, boolean>;
  openRelatedFileDiff: ReturnType<typeof useRelatedFileDiff>["openRelatedFileDiff"];
  rootSessionKey: (rootId: string, sessionKey: string) => any;
  selectedKanbanTaskId: string;
  selectedSession: SessionItem | null;
  selectedSessionRef: MutableRefObject<SessionItem | null>;
  setExpanded: Dispatch<SetStateAction<string[]>>;
  setProjectTreeTabRequest: Dispatch<SetStateAction<{ tab: ProjectTreeTab; nonce: number } | null>>;
  setTaskRelatedFilesById: Dispatch<SetStateAction<Record<string, RelatedFile[]>>>;
  updateSessionRelatedFilesForKey: ReturnType<typeof useSessionMetadata>["updateSessionRelatedFilesForKey"];
}

export function useSessionRelatedFiles(params: UseSessionRelatedFilesParams) {
  const {
    activeBoundSessionKey,
    boundSessionByRootRef,
    cacheVersion,
    currentRootId,
    currentRootIdRef,
    currentSession,
    drawerSessionByRootRef,
    getNodeIdForRoot,
    getSessionSnapshot,
    managedRootByKeyRef,
    managedRootIds,
    multiProjectPendingByKey,
    openRelatedFileDiff,
    rootSessionKey,
    selectedKanbanTaskId,
    selectedSession,
    selectedSessionRef,
    setExpanded,
    setProjectTreeTabRequest,
    setTaskRelatedFilesById,
    updateSessionRelatedFilesForKey,
  } = params;

  const handleSelectedSessionFileClick = useCallback(
    (target: string | RelatedFileClickTarget) => {
      setProjectTreeTabRequest((prev) => ({
        tab: "related",
        nonce: (prev?.nonce || 0) + 1,
      }));
      const root =
        (selectedSessionRef.current?.root_id as string | undefined) ||
        currentRootIdRef.current;
      if (!root) return;
      setExpanded((prev) =>
        Array.from(new Set([...prev, scopeKey(getNodeIdForRoot(root) ?? "", root)])),
      );
      const file =
        typeof target === "string" ? { path: target } : target;
      void openRelatedFileDiff(root, file);
    },
    [openRelatedFileDiff],
  );

  const drawerSessionSnapshot = useMemo(
    () =>
      currentSession ? getSessionSnapshot(currentRootId, currentSession) : null,
    [currentSession, currentRootId, getSessionSnapshot],
  );

  const rootSessionIndicators = useMemo(() => {
    const next: Record<string, { bound?: boolean; pending?: boolean }> = {};
    for (const [scopedRid, dir] of Object.entries(
      managedRootByKeyRef.current as Record<string, any>,
    )) {
      const root = String(dir?.id || "");
      if (!root) continue;
      const nid = scopedRid.includes("::")
        ? scopedRid.slice(0, scopedRid.lastIndexOf("::"))
        : "";
      const boundKey = String(boundSessionByRootRef.current[scopedRid] || "").trim();
      const hasBound = !!boundKey && !boundKey.startsWith("pending-");
      const pendingPrefix = scopeSessionKey(nid, root, "");
      const hasPendingSession = Object.entries(multiProjectPendingByKey).some(
        ([key, pending]) => pending && key.startsWith(pendingPrefix),
      );
      if (!hasBound && !hasPendingSession) {
        continue;
      }
      const drawer = drawerSessionByRootRef.current[scopedRid] as
        | (Session & { pending?: boolean })
        | null
        | undefined;
      const selected =
        ((selectedSession?.root_id as string | undefined) || currentRootId) ===
          root &&
        (selectedSession?.key || selectedSession?.session_key) === boundKey
          ? ((selectedSession as any) as { pending?: boolean })
          : null;
      const pending =
        hasPendingSession ||
        (drawer?.key === boundKey && !!drawer?.pending) ||
        !!selected?.pending;
      next[scopedRid] = { bound: hasBound || hasPendingSession, pending };
    }
    return next;
  }, [
    managedRootIds,
    selectedSession,
    currentSession,
    currentRootId,
    activeBoundSessionKey,
    cacheVersion,
    multiProjectPendingByKey,
    rootSessionKey,
  ]);

  const handleDrawerSessionFileClick = useCallback(
    (target: string | RelatedFileClickTarget) => {
      const root = currentRootIdRef.current;
      if (!root) return;
      const file =
        typeof target === "string" ? { path: target } : target;
      void openRelatedFileDiff(root, file);
    },
    [openRelatedFileDiff],
  );

  const handleRemoveSessionRelatedFile = useCallback(
    async (
      rootID: string | null | undefined,
      sessionKey: string | undefined,
      path: string,
      head = "",
      repoPath = "",
      repoKind = "",
    ) => {
      const resolvedRoot = rootID || currentRootIdRef.current;
      const resolvedKey = sessionKey || "";
      if (!resolvedRoot || !resolvedKey || !path) return;
      const removed = await sessionService.removeSessionRelatedFile(
        resolvedRoot,
        resolvedKey,
        path,
        head,
        repoPath,
        repoKind,
      );
      if (!removed) return;
      const relatedFiles = await sessionService.getSessionRelatedFiles(
        resolvedRoot,
        resolvedKey,
        getNodeIdForRoot(resolvedRoot),
      );
      await setCachedSessionRelatedFiles(resolvedRoot, resolvedKey, relatedFiles, getNodeIdForRoot(resolvedRoot));
      updateSessionRelatedFilesForKey(resolvedRoot, resolvedKey, relatedFiles);
      if (selectedKanbanTaskId) {
        const removedKey = [repoKind || "", repoPath || "", head || "", path || ""].join("\0");
        setTaskRelatedFilesById((prev) => {
          const current = prev[selectedKanbanTaskId] || [];
          return {
            ...prev,
            [selectedKanbanTaskId]: current.filter((file) =>
              [file.repo_kind || "", file.repo_path || "", file.head || "", file.path || ""].join("\0") !== removedKey,
            ),
          };
        });
      }
    },
    [selectedKanbanTaskId, updateSessionRelatedFilesForKey],
  );

  return {
    handleSelectedSessionFileClick,
    drawerSessionSnapshot,
    rootSessionIndicators,
    handleDrawerSessionFileClick,
    handleRemoveSessionRelatedFile,
  };
}
