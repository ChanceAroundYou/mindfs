import { useCallback } from "react";

import { shouldResetSessionLockForRootChange } from "../services/session/sessionLock";
import type { Session } from "../services/session";
import { toSessionItem, type SessionItem } from "./appSession";

export type UseAppRootBindingSettersParams = {
  boundSessionByRootRef: React.RefObject<Record<string, string | null>>;
  scopedRootKey: (rootId: string) => string;
  currentRootIdRef: React.RefObject<string | null>;
  setActiveBoundSessionKey: React.Dispatch<React.SetStateAction<string | null>>;
  drawerSessionByRootRef: React.RefObject<Record<string, SessionItem | null>>;
  setCurrentSession: React.Dispatch<React.SetStateAction<SessionItem | null>>;
  drawerOpenByRootRef: React.RefObject<Record<string, boolean>>;
  setIsDrawerOpen: React.Dispatch<React.SetStateAction<boolean>>;
  selectedSessionByRootRef: React.RefObject<Record<string, string | null>>;
  selectedSessionRef: React.RefObject<SessionItem | null>;
  setSelectedSession: React.Dispatch<React.SetStateAction<SessionItem | null>>;
  setSelectedSessionLoading: React.Dispatch<React.SetStateAction<boolean>>;
  interactionModeRef: React.RefObject<"main" | "drawer">;
  setInteractionMode: React.Dispatch<React.SetStateAction<"main" | "drawer">>;
};

/**
 * 「每个项目各一份」的会话/抽屉状态写入器，以及切项目时的锁复位。
 *
 * 这些状态在 App 里同时存在两份：ref 里按 scopedRootKey 分桶（给非当前项目的
 * 后台会话用），state 里只放当前项目那份（给渲染用）。所以每个 setter 都是
 * 「写 ref → 如果写的是当前项目就同步 state」这个固定形状，五个函数合成一个 hook。
 *
 * resetLocksForRootTransition 是唯一带判断的一个：只有真的换了项目才复位，
 * 同项目内反复触发不能把用户正在看的会话清掉。
 */
export function useAppRootBindingSetters(params: UseAppRootBindingSettersParams) {
  const {
    boundSessionByRootRef,
    scopedRootKey,
    currentRootIdRef,
    setActiveBoundSessionKey,
    drawerSessionByRootRef,
    setCurrentSession,
    drawerOpenByRootRef,
    setIsDrawerOpen,
    selectedSessionByRootRef,
    selectedSessionRef,
    setSelectedSession,
    setSelectedSessionLoading,
    interactionModeRef,
    setInteractionMode,
  } = params;

  const setBoundSessionForRoot = useCallback(
    (rootID: string | null | undefined, key: string | null) => {
      if (!rootID) return;
      boundSessionByRootRef.current[scopedRootKey(rootID)] = key;
      if (currentRootIdRef.current === rootID) {
        setActiveBoundSessionKey(key);
      }
    },
    [],
  );

  const setDrawerSessionForRoot = useCallback(
    (rootID: string | null | undefined, session: Session | SessionItem | null) => {
      if (!rootID) return;
      const next = toSessionItem(rootID, session);
      drawerSessionByRootRef.current[scopedRootKey(rootID)] = next;
      if (currentRootIdRef.current === rootID) {
        setCurrentSession(next);
      }
    },
    [],
  );

  const setDrawerOpenForRoot = useCallback(
    (rootID: string | null | undefined, open: boolean) => {
      if (!rootID) return;
      drawerOpenByRootRef.current[scopedRootKey(rootID)] = open;
      if (currentRootIdRef.current === rootID) {
        setIsDrawerOpen(open);
      }
    },
    [],
  );

  const resetSessionLockForRoot = useCallback(
    (rootID: string | null | undefined) => {
      const root = String(rootID || "").trim();
      if (!root) return;
      setBoundSessionForRoot(root, null);
      setDrawerSessionForRoot(root, null);
      selectedSessionByRootRef.current[scopedRootKey(root)] = null;
      setDrawerOpenForRoot(root, false);
      if (currentRootIdRef.current === root) {
        selectedSessionRef.current = null;
        setSelectedSession(null);
        setSelectedSessionLoading(false);
        interactionModeRef.current = "main";
        setInteractionMode("main");
      }
    },
    [setBoundSessionForRoot, setDrawerOpenForRoot, setDrawerSessionForRoot],
  );

  const resetLocksForRootTransition = useCallback(
    (targetRoot: string | null | undefined) => {
      const sourceRoot = currentRootIdRef.current;
      if (shouldResetSessionLockForRootChange(sourceRoot, targetRoot)) {
        resetSessionLockForRoot(sourceRoot);
        resetSessionLockForRoot(targetRoot);
      }
    },
    [resetSessionLockForRoot],
  );

  return {
    setBoundSessionForRoot,
    setDrawerSessionForRoot,
    setDrawerOpenForRoot,
    resetSessionLockForRoot,
    resetLocksForRootTransition,
  };
}
