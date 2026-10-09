import { useCallback } from "react";
import { sessionService, setCachedSessionRelatedFiles, type RelatedFile, type Session } from "../services/session";
import { mergeRelatedFileGroups, taskIdsForUpdatedSession } from "../services/taskRelatedFiles";
import type { FilePayload } from "../services/file";
import type { SessionItem } from "./appSession";
import type { KanbanTask, TaskDetail } from "../services/tasks";
import { buildSessionJumpTarget, resolveSessionJumpRoot } from "./sessionJump";

/**
 * 看板任务 ↔ 会话抽屉的双向跳转，以及任务关联文件的刷新。
 * 从 App.tsx 抽出（2026-10-08）：这五个函数是同一条「任务 ↔ 会话」链路的
 * 五个入口（点 chip、开抽屉、刷关联文件、刷任务、选中任务），共享
 * taskSessionKeysById / taskDetailsByIdRef 这两张映射表。链路的另一半在
 * app/useTaskWorkspaceState.ts，两边一起看才完整。
 */
export type UseAppTaskDrawerParams = {
  currentRootIdRef: React.RefObject<string | null>;
  file: FilePayload | null;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  handleSelectSession: (session: SessionItem, options?: { preserveTaskSelection?: boolean; preserveMainView?: boolean }) => Promise<void>;
  rootSessionKey: (rootId: string, sessionKey: string) => string;
  sessionCacheRef: React.RefObject<Record<string, Session>>;
  sessions: SessionItem[];
  setSelectedKanbanTaskId: React.Dispatch<React.SetStateAction<string>>;
  setTaskRelatedFilesById: React.Dispatch<React.SetStateAction<Record<string, RelatedFile[]>>>;
  taskDetailsByIdRef: React.RefObject<Record<string, TaskDetail>>;
  taskSessionKeysById: Record<string, string[]>;
  taskSessionKeysByIdRef: React.RefObject<Record<string, string[]>>;
  updateSessionRelatedFilesForKey: (rootID: string, sessionKey: string, relatedFiles: RelatedFile[]) => void;
};

/**
 * handleSelectSession 的 options 形如 preserveTaskSelection：从任务跳进会话时
 * 要保住看板上的任务高亮，否则用户回不去。
 */
export function useAppTaskDrawer(params: UseAppTaskDrawerParams) {
  const { currentRootIdRef, file, getNodeIdForRoot, handleSelectSession, rootSessionKey, sessionCacheRef, sessions, setSelectedKanbanTaskId, setTaskRelatedFilesById, taskDetailsByIdRef, taskSessionKeysById, taskSessionKeysByIdRef, updateSessionRelatedFilesForKey } = params;

  const handleSessionChipClick = useCallback(
    (sessionKey: string, rootOverride?: string | null) => {
      if (!sessionKey) return;
      const matched = sessions.find((item) => {
        const key = item.key || item.session_key;
        return key === sessionKey;
      });
      // CUSTOM(G-Z): 归属按卡片/文件视图声明的 root 钉死，不随当前根漂移（见 sessionJump.ts）。
      const target = buildSessionJumpTarget({
        sessionKey,
        rootOverride: rootOverride ?? file?.root,
        matched,
        currentRoot: currentRootIdRef.current,
      });
      if (!target) return;
      handleSelectSession(target);
    },
    [file, sessions, handleSelectSession],
  );

	  const handleTaskSessionDrawerOpen = useCallback(
    (sessionKey: string, rootOverride?: string | null, taskId?: string) => {
      const key = String(sessionKey || "").trim();
      if (!key) return;
      const matched = sessions.find((item) => (item.key || item.session_key) === key);
      // 任务会话就是普通会话：直接走主面板装配（selectedSession + main 模式 + URL + 同步），
      // 不再走 drawer 浮层——drawer 模式下在会话面板里切换工作台/看板没有全屏语境。
      const root = resolveSessionJumpRoot(rootOverride, matched, currentRootIdRef.current);
      if (!root) return;
      const cached = sessionCacheRef.current[rootSessionKey(root, key)];
      // CUSTOM(G-Z): 缓存里可能是历史写入的无 root 对象，这里强制写回卡片 root，
      // 否则 handleSelectSession 的 `root_id || 当前根` 兜底会把它认成当前项目。
      const target = buildSessionJumpTarget({
        sessionKey: key,
        rootOverride,
        taskId,
        matched,
        cached,
        currentRoot: currentRootIdRef.current,
      });
      if (!target) return;
      void handleSelectSession(target as any, { preserveTaskSelection: true });
    },
    [
      currentRootIdRef,
      handleSelectSession,
      rootSessionKey,
      sessionCacheRef,
      sessions,
    ],
  );

  const refreshTaskRelatedFiles = useCallback(async (
	    root: string,
	    taskId: string,
	    sessionKeys: string[],
	  ) => {
	    const keys = Array.from(new Set(
	      sessionKeys
	        .map((key) => String(key || "").trim())
	        .filter(Boolean),
	    ));
	    if (!root || !taskId || keys.length === 0) return;
	    const relatedFileGroups = await Promise.all(
	      keys.map(async (sessionKey) => {
	        const relatedFiles = await sessionService.getSessionRelatedFiles(root, sessionKey, getNodeIdForRoot(root));
	        await setCachedSessionRelatedFiles(root, sessionKey, relatedFiles, getNodeIdForRoot(root));
	        updateSessionRelatedFilesForKey(root, sessionKey, relatedFiles);
	        return relatedFiles;
	      }),
	    );
	    setTaskRelatedFilesById((prev) => ({ ...prev, [taskId]: mergeRelatedFileGroups(relatedFileGroups) }));
	  }, [updateSessionRelatedFilesForKey]);

	  const refreshTasksForRelatedSession = useCallback((root: string, sessionKey: string) => {
	    const taskIds = taskIdsForUpdatedSession(taskSessionKeysByIdRef.current, sessionKey);
	    taskIds.forEach((taskId) => {
	      const detail = taskDetailsByIdRef.current[taskId];
	      const task = detail?.task;
	      if (!task || task.root_id !== root) return;
	      const sessionKeys = Array.from(new Set(
	        [...(taskSessionKeysByIdRef.current[taskId] || []), task.main_session_key]
	          .map((key) => String(key || "").trim())
	          .filter(Boolean),
	      ));
	      void refreshTaskRelatedFiles(root, taskId, sessionKeys).catch((error) => {
	        console.error("[task.related_files] refresh from session event failed", { root, taskId, sessionKey, error });
	      });
	    });
	  }, [refreshTaskRelatedFiles]);

	  const handleSelectKanbanTask = useCallback((task: KanbanTask) => {
	    const taskId = String(task.id || "");
	    if (!taskId) return;
	    setSelectedKanbanTaskId((prev) => prev === taskId ? "" : taskId);
	    const root = task.root_id || currentRootIdRef.current || "";
	    const sessionKeys = Array.from(new Set(
	      [...(taskSessionKeysById[taskId] || []), task.main_session_key]
	        .map((key) => String(key || "").trim())
	        .filter(Boolean),
	    ));
	    if (!root || sessionKeys.length === 0) return;
	    void refreshTaskRelatedFiles(root, taskId, sessionKeys)
	      .catch((error) => {
	        console.error("[task.related_files] failed", { root, taskId, sessionKeys, error });
	      });
	  }, [refreshTaskRelatedFiles, taskSessionKeysById]);

  return {
    handleSessionChipClick,
    handleTaskSessionDrawerOpen,
    refreshTaskRelatedFiles,
    refreshTasksForRelatedSession,
    handleSelectKanbanTask,
  };
}
