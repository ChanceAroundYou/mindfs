/**
 * 会话列表与文件树顶栏的用户动作 —— A8 簇前半（~171 行）。
 *
 * 覆盖：新建空文件、上传目录到当前选中目录、选中会话并关抽屉、会话置顶、
 * 归档/删除时从本地状态摘除整棵子树。
 *
 * 为什么这些放一起：它们共享同一块状态面（selectedDirRef / fileRef /
 * expanded / refreshTreeDir / sessionsRef / 多项目分组 ref），都是「列表或
 * 文件树上的一次用户点击」，拆得更细只会让参数面重复十几遍。
 *
 * 做法：hook，参数面 UseSessionListActionsParams 即完整契约；函数体逐字搬入
 * （保持源码正则断言可命中，配合 source-map 注册）。
 */
import { useCallback, type MutableRefObject, type Dispatch, type SetStateAction } from "react";
import { useI18n } from "../i18n";
import { MultiProjectSessionGroup, SessionItem } from "./appSession";
import { useSessionLifecycle } from "./useSessionLifecycle";
import { useFileRefresh } from "./useFileRefresh";
import { appPath } from "../services/net/base";
import { alertDialog, promptDialog } from "../services/dialog";
import { ProtectedAPIError, protectedJSON as apiProtectedJSON } from "../services/net/api";
import { reportError } from "../services/net/error";
import { optimisticSessionPin, setSessionPin, writePins } from "../services/pins";
import { collectSessionSubtreeKeys, type SessionTreeItem } from "../services/sessionTree";
import { expandKey, scopeKey } from "../shared/scope";
import { dirnameOfPath } from "./appPath";
import { invalidateFileCache, type FilePayload } from "../services/file";
import { isUploadAbortError, uploadFiles, type UploadProgress } from "../services/upload";

export interface UseSessionListActionsParams {
  currentRootIdRef: MutableRefObject<string | null>;
  directoryUploadAbortRef: MutableRefObject<AbortController | null>;
  fileRef: MutableRefObject<FilePayload | null>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  isMobile: boolean;
  multiProjectSessionGroupsRef: MutableRefObject<MultiProjectSessionGroup[]>;
  selectedDirRef: MutableRefObject<string | null>;
  sessionsRef: MutableRefObject<SessionItem[]>;
  setDirectoryUploadProgress: Dispatch<SetStateAction<UploadProgress | null>>;
  setExpanded: Dispatch<SetStateAction<string[]>>;
  setIsRightOpen: Dispatch<SetStateAction<boolean>>;
  setMultiProjectSessionGroups: Dispatch<SetStateAction<MultiProjectSessionGroup[]>>;
  setSessions: Dispatch<SetStateAction<SessionItem[]>>;
  handleSelectSession: ReturnType<typeof useSessionLifecycle>["handleSelectSession"];
  refreshTreeDir: ReturnType<typeof useFileRefresh>["refreshTreeDir"];
  t: ReturnType<typeof useI18n>["t"];
}

export function useSessionListActions(params: UseSessionListActionsParams) {
  const {
    currentRootIdRef,
    directoryUploadAbortRef,
    fileRef,
    getNodeIdForRoot,
    isMobile,
    multiProjectSessionGroupsRef,
    selectedDirRef,
    sessionsRef,
    setDirectoryUploadProgress,
    setExpanded,
    setIsRightOpen,
    setMultiProjectSessionGroups,
    setSessions,
    handleSelectSession,
    refreshTreeDir,
    t,
  } = params;

  const handleCreateBlankFile = useCallback(async () => {
    const rootID = currentRootIdRef.current;
    if (!rootID) return;
    const targetDir = (selectedDirRef.current === rootID ? "." : selectedDirRef.current) || ".";
    const input = await promptDialog({ message: t("directory.fileNamePrompt") });
    if (input === null) return;
    const name = input.trim();
    if (!name || name === "." || name === ".." || /[/\\\x00]/.test(name)) {
      alertDialog(t("directory.invalidFileName"));
      return;
    }
    try {
      await apiProtectedJSON(appPath(`/api/file?${new URLSearchParams({ root: rootID })}`), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ dir: targetDir, name }),
      });
      const currentDir = (selectedDirRef.current === rootID ? "." : selectedDirRef.current) || ".";
      await refreshTreeDir(rootID, targetDir, rootID === currentRootIdRef.current && currentDir === targetDir);
    } catch (err) {
      reportError("file.write_failed", err instanceof ProtectedAPIError && err.status === 409
        ? t("directory.fileExists")
        : String((err as Error)?.message || t("directory.createFileFailed")));
    }
  }, [refreshTreeDir, t]);

  const handleTreeUpload = useCallback(
    async (files: File[]) => {
      const rootID = currentRootIdRef.current;
      if (!rootID || files.length === 0) return;

      const selectedDirPath =
        selectedDirRef.current === rootID ? "." : selectedDirRef.current;
      const targetDir =
        selectedDirPath ||
        (fileRef.current?.path ? dirnameOfPath(fileRef.current.path) : ".");
      setDirectoryUploadProgress(null);
      try {
        const uploadAbort = new AbortController();
        directoryUploadAbortRef.current = uploadAbort;
        const uploaded = await uploadFiles({
          rootId: rootID,
          dir: targetDir,
          files,
          onProgress: setDirectoryUploadProgress,
          signal: uploadAbort.signal,
          nodeId: getNodeIdForRoot(rootID),
        });
        uploaded.forEach((item) => {
          if (typeof item?.path === "string" && item.path) {
            invalidateFileCache(rootID, item.path);
          }
        });
        const currentDir =
          (selectedDirRef.current === rootID ? "." : selectedDirRef.current) ||
          ".";
        const syncMain =
          rootID === currentRootIdRef.current && currentDir === targetDir;
        await refreshTreeDir(rootID, targetDir, syncMain);
        setExpanded((prev) =>
          Array.from(
            new Set([
              ...prev,
              scopeKey(getNodeIdForRoot(rootID) ?? "", rootID),
              expandKey(
                getNodeIdForRoot(rootID),
                rootID,
                targetDir === "." ? rootID : targetDir,
                targetDir === ".",
              ),
            ]),
          ),
        );
      } catch (err) {
        if (!isUploadAbortError(err)) {
          reportError(
            "file.write_failed",
            String((err as Error)?.message || t("file.uploadFailed")),
          );
        }
      } finally {
        directoryUploadAbortRef.current = null;
        setDirectoryUploadProgress(null);
      }
    },
    [refreshTreeDir, t],
  );



  // 稳定回调：SessionList/SessionCard 的 memo 依赖 props 引用稳定（onSelect 之前是 inline 箭头，每次父渲染都变）。
  const handleSelectSessionAndClose = useCallback(
    (session: SessionItem) => {
      void handleSelectSession(session);
      if (isMobile) setIsRightOpen(false);
    },
    [handleSelectSession, isMobile],
  );



  const handlePinSession = useCallback(
    async (session: SessionItem, pinned: boolean) => {
      const sessionKey = session?.key || session?.session_key;
      const rootID =
        (session?.root_id as string | undefined) || currentRootIdRef.current;
      if (!rootID || !sessionKey) return false;

      // 乐观更新 store：用户点了就该马上看到，不必等往返。
      // store 是排序的唯一来源，所以这一下就足以让列表重排 —— 下面那些
      // setSessions/setMultiProjectSessionGroups 逐条打 pinned_at 的写法
      // 曾经是第三份能重排列表的状态，与 store 各排一次就是狂跳的来源。
      const rollback = optimisticSessionPin(rootID, sessionKey, pinned);
      // 写**主节点**的置顶表（控制面），不是当前选中节点的那个会话库 ——
      // 后者已退役 pinned_at，写进去就回到「按机器分」的老形状。
      try {
        const next = await setSessionPin(rootID, sessionKey, pinned);
        // 用服务端落库后的整张表覆盖（权威），不再本地合并 ——
        // 合并两份状态正是「刷新就变回去」的来源。
        writePins(next);
        return true;
      } catch {
        rollback();
        reportError("session.pin_failed", t("session.pinFailed"));
        return false;
      }
    },
    [t],
  );

  // 归档/取消归档。后端按 parent_session_key 级联整棵子树，前端要摘掉的是同一批 key
  // ——和 handleDeleteSession 完全同一套收集+摘除逻辑，只是摘除后不销毁缓存（内容还在）。
  const pruneSubtreeFromSessionState = useCallback(
    (rootID: string, sessionKey: string, sessionNodeId: string) => {
      const knownItems: SessionTreeItem[] = [
        ...sessionsRef.current,
        ...multiProjectSessionGroupsRef.current.flatMap((group) => group.sessions || []),
      ];
      const affected = new Set<string>(
        collectSessionSubtreeKeys(knownItems, sessionKey),
      );
      if (affected.size <= 0) {
        return affected;
      }
      setSessions((prev) =>
        prev.filter((item) => !affected.has(item.key || item.session_key || "")),
      );
      setMultiProjectSessionGroups((prev) =>
        prev
          .map((group) => {
            if (
              group.rootId !== rootID ||
              (sessionNodeId && (group as any)._nodeId !== sessionNodeId)
            ) {
              return group;
            }
            const sessions = group.sessions.filter(
              (item) => !affected.has(item.key || item.session_key || ""),
            );
            return {
              ...group,
              sessions,
              totalCount: Math.max(0, group.totalCount - (group.sessions.length - sessions.length)),
            };
          })
          .filter((group) => group.totalCount > 0 || group.sessions.length > 0),
      );
      return affected;
    },
    [],
  );

  return {
    handleCreateBlankFile,
    handleTreeUpload,
    handleSelectSessionAndClose,
    handlePinSession,
    pruneSubtreeFromSessionState,
  };
}
