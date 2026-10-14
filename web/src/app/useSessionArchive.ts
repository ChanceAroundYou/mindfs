/**
 * 会话删除与归档 —— A1b 簇（2 个单元，~210 行）。
 *
 * 与 A1a 同域，但依赖 L3053 之后才声明的闭包（setSessionSearchResults、
 * pruneSubtreeFromSessionState），放不进 A1a 的 TDZ 窗口，故单独成 hook。
 *
 * 做法：hook，参数面 UseSessionArchiveParams 即完整契约；函数体逐字搬入
 * （保持源码正则断言可命中，配合 source-map 注册）。
 */
import { useCallback, useMemo, type MutableRefObject, type Dispatch, type SetStateAction } from "react";
import { ManagedRootPayload, useResponsive } from "./appMisc";
import { URLState } from "./appPath";
import { MultiProjectSessionGroup, PendingSend, SessionItem, toSessionItem } from "./appSession";
import { useActionHandlers } from "./useActionHandlers";
import { useCompletionSound } from "./useCompletionSound";
import { useExternalSessionImport } from "./useExternalSessionImport";
import { useGitActions } from "./useGitActions";
import { useGitData } from "./useGitData";
import { useMultiProjectSessions } from "./useMultiProjectSessions";
import { useProjectLifecycle } from "./useProjectLifecycle";
import { useProjectTreeWorktrees } from "./useProjectTreeWorktrees";
import { useSendMessage } from "./useSendMessage";
import { useSessionCache } from "./useSessionCache";
import { useSessionLifecycle } from "./useSessionLifecycle";
import { useSessionSearch } from "./useSessionSearch";
import { useSessionSidebarView } from "./useSessionSidebarView";
import { useSessionStreamCache } from "./useSessionStreamCache";
import { useTaskTemplates } from "./useTaskTemplates";
import { useI18n } from "../i18n";
import { confirmDialog } from "../services/dialog";
import { type FilePayload } from "../services/file";
import { reportError } from "../services/net/error";
import { type Session, deleteCachedSession, deleteCachedSessionLists, sessionService } from "../services/session";
import { mergeSessionItems } from "../services/session/sessionListMerge";
import { type SessionTreeItem, collectSessionSubtreeKeys } from "../shared/sessionTree";

export type UseSessionArchiveParams = {
  boundSessionByRootRef: MutableRefObject<Record<string, string | null>>;
  bumpCacheVersion: () => any;
  cancelRequestedBySessionRef: MutableRefObject<Record<string, boolean>>;
  currentRootIdRef: MutableRefObject<string | null>;
  drawerSessionByRootRef: MutableRefObject<Record<string, SessionItem | null>>;
  file: FilePayload | null;
  fileCursorRef: MutableRefObject<number>;
  fileRef: MutableRefObject<FilePayload | null>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  loadedSessionRef: ReturnType<typeof useSessionCache>["loadedSessionRef"];
  loadingSessionRef: ReturnType<typeof useSessionCache>["loadingSessionRef"];
  managedRootByIdRef: MutableRefObject<Record<string, ManagedRootPayload>>;
  multiProjectSessionGroupsRef: MutableRefObject<MultiProjectSessionGroup[]>;
  pendingBySessionRef: MutableRefObject<Record<string, PendingSend>>;
  pluginQuery: Record<string, string>;
  pruneSubtreeFromSessionState: (rootID: string, sessionKey: string, sessionNodeId: string) => any;
  replaceURLState: (next: URLState) => any;
  resetSessionLockForRoot: (rootID: string | null | undefined) => any;
  rootSessionKey: (rootId: string, sessionKey: string) => any;
  scopedRootKey: (rootId: string) => string;
  selectedSessionByRootRef: MutableRefObject<Record<string, string | null>>;
  selectedSessionRef: MutableRefObject<SessionItem | null>;
  sessionCacheRef: ReturnType<typeof useSessionCache>["sessionCacheRef"];
  sessions: SessionItem[];
  sessionsRef: MutableRefObject<SessionItem[]>;
  setArchiveReloadToken: Dispatch<SetStateAction<number>>;
  setArchivedGroups: Dispatch<SetStateAction<MultiProjectSessionGroup[]>>;
  setBoundSessionForRoot: (rootID: string | null | undefined, key: string | null) => any;
  setDrawerOpenForRoot: (rootID: string | null | undefined, open: boolean) => any;
  setDrawerSessionForRoot: (rootID: string | null | undefined, session: Session | SessionItem | null) => any;
  setMultiProjectSessionGroups: Dispatch<SetStateAction<MultiProjectSessionGroup[]>>;
  setMultiProjectSessionPending: (rootID: string | null | undefined, sessionKey: string | null | undefined, pending: boolean) => any;
  setSelectedSession: Dispatch<SetStateAction<SessionItem | null>>;
  setSelectedSessionLoading: Dispatch<SetStateAction<boolean>>;
  setSessionSearchResults: ReturnType<typeof useSessionSearch>["setSessionSearchResults"];
  setSessions: Dispatch<SetStateAction<SessionItem[]>>;
  staleSessionKeysRef: ReturnType<typeof useSessionCache>["staleSessionKeysRef"];
};

export function useSessionArchive(params: UseSessionArchiveParams) {
  const { t } = useI18n();
  const {
    boundSessionByRootRef,
    bumpCacheVersion,
    cancelRequestedBySessionRef,
    currentRootIdRef,
    drawerSessionByRootRef,
    file,
    fileCursorRef,
    fileRef,
    getNodeIdForRoot,
    loadedSessionRef,
    loadingSessionRef,
    managedRootByIdRef,
    multiProjectSessionGroupsRef,
    pendingBySessionRef,
    pluginQuery,
    pruneSubtreeFromSessionState,
    replaceURLState,
    resetSessionLockForRoot,
    rootSessionKey,
    scopedRootKey,
    selectedSessionByRootRef,
    selectedSessionRef,
    sessionCacheRef,
    sessions,
    sessionsRef,
    setArchiveReloadToken,
    setArchivedGroups,
    setBoundSessionForRoot,
    setDrawerOpenForRoot,
    setDrawerSessionForRoot,
    setMultiProjectSessionGroups,
    setMultiProjectSessionPending,
    setSelectedSession,
    setSelectedSessionLoading,
    setSessionSearchResults,
    setSessions,
    staleSessionKeysRef,
  } = params;

  const handleDeleteSession = useCallback(
    async (session: SessionItem) => {
      const sessionKey = session?.key || session?.session_key;
      const rootID =
        (session?.root_id as string | undefined) || currentRootIdRef.current;
      if (!rootID || !sessionKey) return;

      // 删除是不可撤销的（归档才是那条可回头的路），且后端会连带删掉整棵子树。
      // 放在唯一的删除出口而不是每个调用点，菜单/快捷键/未来入口全都覆盖到。
      const sessionName = String(session?.name || sessionKey.slice(0, 8));
      if (
        !(await confirmDialog({
          message: t("sessionList.confirmDeleteSession", { name: sessionName }),
          danger: true,
        }))
      ) {
        return;
      }

      const deleted = await sessionService.deleteSession(rootID, sessionKey, String((session as any)?._nodeId || "") || getNodeIdForRoot(rootID));
      if (!deleted) {
        reportError("session.delete_failed", t("session.deleteFailed"));
        return;
      }

      // 后端已按 parent_session_key 级联删掉整棵子树；这里要在**全部**本地状态里
      // 摘掉同一批 key。只看 sessionsRef 会漏掉只存在于多项目分组里的子会话，
      // 漏掉的那些会被树构建当成孤儿提升成顶层整宽行 —— 面板就此撑爆且收不回去。
      const knownItems: SessionTreeItem[] = [
        ...sessionsRef.current,
        ...multiProjectSessionGroupsRef.current.flatMap((group) => group.sessions || []),
      ];
      const deletedKeys = new Set<string>(collectSessionSubtreeKeys(knownItems, sessionKey));

      setSessions((prev) =>
        prev.filter((item) => !deletedKeys.has(item.key || item.session_key || "")),
      );

      for (const deletedKey of deletedKeys) {
        const cacheKey = rootSessionKey(rootID, deletedKey);
        delete sessionCacheRef.current[cacheKey];
        delete loadedSessionRef.current[cacheKey];
        delete loadingSessionRef.current[cacheKey];
        delete pendingBySessionRef.current[cacheKey];
        delete cancelRequestedBySessionRef.current[cacheKey];
        staleSessionKeysRef.current.delete(cacheKey);
        void deleteCachedSession(rootID, deletedKey, getNodeIdForRoot(rootID));
      }

      // 搜索结果独立于两棵列表 state，删掉的会话会继续挂在结果里；
      // 列表快照同理——只删单个会话记录不动列表快照的话，下次进面板已删的行会从缓存里复活。
      setSessionSearchResults((prev) =>
        prev.filter((item) => !deletedKeys.has(item.key || item.session_key || "")),
      );
      void deleteCachedSessionLists(rootID, getNodeIdForRoot(rootID));

      if (deletedKeys.has(boundSessionByRootRef.current[scopedRootKey(rootID)] || "")) {
        resetSessionLockForRoot(rootID);
      }
      if (deletedKeys.has(selectedSessionByRootRef.current[scopedRootKey(rootID)] || "")) {
        selectedSessionByRootRef.current[scopedRootKey(rootID)] = null;
      }
      if (deletedKeys.has(drawerSessionByRootRef.current[scopedRootKey(rootID)]?.key || "")) {
        setDrawerSessionForRoot(rootID, null);
        setDrawerOpenForRoot(rootID, false);
      }
      setMultiProjectSessionGroups((prev) =>
        prev.map((group) => {
          if (group.rootId !== rootID || ((session as any)?._nodeId && (group as any)._nodeId !== (session as any)?._nodeId)) {
            return group;
          }
          const sessions = group.sessions.filter(
            (item) => !deletedKeys.has(item.key || item.session_key || ""),
          );
          return {
            ...group,
            sessions,
            totalCount: Math.max(0, group.totalCount - (group.sessions.length - sessions.length)),
          };
        }).filter((group) => group.totalCount > 0 || group.sessions.length > 0),
      );
      for (const deletedKey of deletedKeys) {
        setMultiProjectSessionPending(rootID, deletedKey, false);
      }

      const selectedKey =
        selectedSessionRef.current?.key ||
        selectedSessionRef.current?.session_key;
      const selectedRoot =
        (selectedSessionRef.current?.root_id as string | undefined) ||
        currentRootIdRef.current;
      if (deletedKeys.has(selectedKey || "") && selectedRoot === rootID) {
        setSelectedSession(null);
        setSelectedSessionLoading(false);
        replaceURLState({
          root: rootID,
          file: fileRef.current?.root === rootID ? fileRef.current.path : "",
          session: "",
          cursor: fileCursorRef.current || 0,
          pluginQuery: fileRef.current?.root === rootID ? pluginQuery : {},
        });
      }

      bumpCacheVersion();
    },
    [
      bumpCacheVersion,
      pluginQuery,
      replaceURLState,
      rootSessionKey,
      setBoundSessionForRoot,
      setDrawerOpenForRoot,
      setDrawerSessionForRoot,
      setMultiProjectSessionPending,
      resetSessionLockForRoot,
    ],
  );
  const handleArchiveSession = useCallback(
    async (session: SessionItem, archived: boolean) => {
      const sessionKey = session?.key || session?.session_key;
      const rootID =
        (session?.root_id as string | undefined) || currentRootIdRef.current;
      if (!rootID || !sessionKey) return false;
      const sessionNodeId =
        String((session as any)?._nodeId || "") ||
        getNodeIdForRoot(rootID) ||
        "";

      const updated = await sessionService.setSessionArchived(
        rootID,
        sessionKey,
        archived,
        sessionNodeId,
      );
      if (!updated) {
        reportError("session.archive_failed", t("session.archiveFailed"));
        return false;
      }

      // 归档态只由归档区的懒加载结果持有，不进两棵列表 state ——
      // 同一批 key 出现在两个视图里会被树构建各算一次，还会撞上孤儿提升。
      const affected = pruneSubtreeFromSessionState(
        rootID,
        sessionKey,
        sessionNodeId,
      );
      if (!archived) {
        // 只把这个会话放回主列表：归档时它的子会话已经被真删掉了，
        // 连后代一起恢复会凭空造出一批服务端不存在的行（它们早就没有正文了）。
        const restored = [
          toSessionItem(rootID, {
            ...(session as any),
            key: sessionKey,
            session_key: sessionKey,
            root_id: rootID,
            _nodeId: sessionNodeId,
            archived_at: null,
          }),
        ].filter((item): item is SessionItem => !!item);
        if (restored.length) {
          setSessions((prev) => mergeSessionItems(prev, restored));
          setMultiProjectSessionGroups((prev) => {
            const matches = (group: MultiProjectSessionGroup) =>
              group.rootId === rootID &&
              (!sessionNodeId || (group as any)._nodeId === sessionNodeId);
            // 分组可能在上面的摘除里被整个丢掉（该项目可见会话归零），
            // 多项目面板只渲染分组，所以取消归档必须把它建回来。
            const next = prev.some(matches)
              ? prev
              : [
                  ...prev,
                  {
                    rootId: rootID,
                    rootName:
                      managedRootByIdRef.current[rootID]?.display_name || rootID,
                    latestSessionTime: String(session.updated_at || ""),
                    _nodeId: sessionNodeId || undefined,
                    sessions: [],
                    totalCount: 0,
                  } satisfies MultiProjectSessionGroup,
                ];
            return next.map((group) =>
              matches(group)
                ? {
                    ...group,
                    sessions: mergeSessionItems(group.sessions, restored),
                    totalCount: group.totalCount + restored.length,
                  }
                : group,
            );
          });
        }
      }
      setSessionSearchResults((prev) =>
        prev.filter((item) => !affected.has(item.key || item.session_key || "")),
      );
      // 归档区快照已过期：取消归档会往里加行，归档会摘掉一行。计数也别手动加减 ——
      // 下面的 effect 是唯一的真相源，重拉一次比在这里猜差多少可靠。
      setArchivedGroups((prev) =>
        prev
          .map((group) => ({
            ...group,
            sessions: group.sessions.filter(
              (item) => !affected.has(item.key || item.session_key || ""),
            ),
          }))
          .filter((group) => group.sessions.length > 0),
      );
      setArchiveReloadToken((token) => token + 1);
      void deleteCachedSessionLists(rootID, sessionNodeId);
      return true;
    },
    [pruneSubtreeFromSessionState, t],
  );

  return {
    handleDeleteSession,
    handleArchiveSession,
  };
}
