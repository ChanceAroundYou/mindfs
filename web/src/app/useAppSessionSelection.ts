import { useCallback, useMemo } from "react";

import { persistFileScrollPositions } from "./appStorage";
import { sessionService } from "../services/session";
import { basenameOfPath, buildFileScrollKey } from "./appPath";
import type { FilePayload } from "../services/file/types";
import type { GitDiffPayload } from "../services/git/types";
import type { SessionItem } from "./appSession";
import type { MainViewMode } from "./appPath";

export type UseAppSessionSelectionParams = {
  currentRootIdRef: React.RefObject<string | null>;
  boundSessionByRootRef: React.RefObject<Record<string, string | null>>;
  scopedRootKey: (rootId: string) => string;
  suppressedAutoBindSessionByRootRef: React.RefObject<Record<string, string | null>>;
  switchMainView: (mode: MainViewMode) => void;
  selectedSessionRef: React.RefObject<SessionItem | null>;
  currentSessionRef: React.RefObject<SessionItem | null>;
  interactionModeRef: React.RefObject<"main" | "drawer">;
  setSelectedSession: React.Dispatch<React.SetStateAction<SessionItem | null>>;
  selectedSessionByRootRef: React.RefObject<Record<string, string | null>>;
  resetSessionLockForRoot: (rootID: string | null | undefined) => void;
  rootSessionKey: (rootId: string, sessionKey: string) => string;
  cancelRequestedBySessionRef: React.RefObject<Record<string, boolean>>;
  clearLocalPendingForSession: (
    rootID: string | null | undefined,
    sessionKey: string | null | undefined,
  ) => void;
  file: FilePayload | null;
  gitDiff: GitDiffPayload | null;
  fileRef: React.RefObject<FilePayload | null>;
  fileScrollPositionsRef: React.RefObject<Record<string, number>>;
};

/**
 * 会话选择的四个入口：取消当前回合、开新会话、当前选中项的来源、记住文件滚动位置。
 *
 * 共同点是都在动「当前选中的是什么」这一组状态 —— 会话、文件、git diff、
 * 交互模式。它们散在 App 里时，每个都只看得见自己动的那一两个 ref，
 * 改其中一个很容易漏掉另一个（比如开新会话忘了清 interactionMode）。
 */
export function useAppSessionSelection(params: UseAppSessionSelectionParams) {
  const {
    currentRootIdRef,
    boundSessionByRootRef,
    scopedRootKey,
    suppressedAutoBindSessionByRootRef,
    switchMainView,
    selectedSessionRef,
    currentSessionRef,
    interactionModeRef,
    setSelectedSession,
    selectedSessionByRootRef,
    resetSessionLockForRoot,
    rootSessionKey,
    cancelRequestedBySessionRef,
    clearLocalPendingForSession,
    file,
    gitDiff,
    fileRef,
    fileScrollPositionsRef,
  } = params;

  const handleCancelCurrentTurn = useCallback(
    async (sessionKey: string) => {
      const activeRoot = currentRootIdRef.current;
      if (!activeRoot || !sessionKey) return;
      const cacheKey = rootSessionKey(activeRoot, sessionKey);
      cancelRequestedBySessionRef.current[cacheKey] = true;
      const sent = await sessionService.cancelMessage(activeRoot, sessionKey);
      if (!sent) {
        delete cancelRequestedBySessionRef.current[cacheKey];
        return;
      }
      clearLocalPendingForSession(activeRoot, sessionKey);
    },
    [clearLocalPendingForSession, rootSessionKey],
  );

  const handleNewSession = useCallback(() => {
    const rootID = currentRootIdRef.current;
    const previousBoundKey = rootID ? boundSessionByRootRef.current[scopedRootKey(rootID)] : "";
    if (rootID && previousBoundKey && !previousBoundKey.startsWith("pending-")) {
      suppressedAutoBindSessionByRootRef.current[scopedRootKey(rootID)] = previousBoundKey;
    }
    switchMainView("chat");
    selectedSessionRef.current = null;
    currentSessionRef.current = null;
    interactionModeRef.current = "main";
    setSelectedSession(null);
    if (rootID) {
      selectedSessionByRootRef.current[scopedRootKey(rootID)] = null;
    }
    resetSessionLockForRoot(rootID);
  }, [
    resetSessionLockForRoot,
    switchMainView,
  ]);

  const currentSelectionSource = useMemo(() => {
    if (file?.path) {
      return {
        path: file.path,
        name: file.name || basenameOfPath(file.path),
      };
    }
    if (gitDiff?.path) {
      return {
        path: gitDiff.path,
        name: basenameOfPath(gitDiff.path),
      };
    }
    return null;
  }, [file, gitDiff]);

  const rememberCurrentFileScroll = useCallback(() => {
    const currentFile = fileRef.current;
    const key = buildFileScrollKey(
      currentFile?.root || currentRootIdRef.current,
      currentFile?.path,
    );
    if (!key) return;
    if (!(key in fileScrollPositionsRef.current)) {
      fileScrollPositionsRef.current[key] = 0;
      persistFileScrollPositions(fileScrollPositionsRef.current);
    }
  }, []);

  return {
    handleCancelCurrentTurn,
    handleNewSession,
    currentSelectionSource,
    rememberCurrentFileScroll,
  };
}