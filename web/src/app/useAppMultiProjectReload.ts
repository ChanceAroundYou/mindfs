import { useCallback, useEffect, useRef } from "react";
import type { MultiProjectSessionGroup } from "./appSession";
import type { UseAppSessionLayerParams } from "./useAppSessionLayerTypes";
import { useReplyingSessionPoll } from "./useReplyingSessionPoll";

export function useAppMultiProjectReload({
  MULTI_PROJECT_RELOAD_DEBOUNCE_MS,
  applyPendingToMultiProjectGroups,
  currentRootNodeIdRef,
  loadMultiProjectSessionGroups,
  managedRootByKeyRef,
  multiProjectPendingRef,
  multiProjectAwaitingRef,
  multiProjectSessionsEnabled,
  setMultiProjectPendingByKey,
  setMultiProjectAwaitingByKey,
  setMultiProjectSessionGroups,
}: {
  MULTI_PROJECT_RELOAD_DEBOUNCE_MS: number;
  applyPendingToMultiProjectGroups: (groups: MultiProjectSessionGroup[], pending: Record<string, boolean>, awaiting?: Record<string, boolean>) => MultiProjectSessionGroup[];
  currentRootNodeIdRef: React.RefObject<string | null>;
  loadMultiProjectSessionGroups: () => Promise<void>;
  managedRootByKeyRef: UseAppSessionLayerParams["managedRootByKeyRef"];
  multiProjectPendingRef: React.RefObject<Record<string, boolean>>;
  multiProjectAwaitingRef: React.RefObject<Record<string, boolean>>;
  multiProjectSessionsEnabled: true;
  setMultiProjectPendingByKey: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
  setMultiProjectAwaitingByKey: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
  setMultiProjectSessionGroups: React.Dispatch<React.SetStateAction<MultiProjectSessionGroup[]>>;
}) {
  const multiProjectReloadTimerRef = useRef<number | null>(null);

  /**
   * 合并窗口内多次「多项目会话重拉」为最后一次。
   *
   * 什么时候会连发：session.created 现在是**所有**建会话路径的统一信号了
   * （看板任务、定时任务、子会话、fork），而一次看板动作可能连带建多个会话；
   * agent 密集起子代理时更是一串 created。原来这些路径压根不发 created，所以
   * 裸调 loadMultiProjectSessionGroups 不觉得多；统一信号一加，
   * 每次 created 都会扇出一次 `/api/sessions?multi_root=1`（逐节点各一次），
   * 密集建会话时能把接口打爆。
   *
   * 窗口取 300ms，与 scheduleSessionListReload 同一档：单个节点的
   * `/api/sessions?multi_root=1` 实测 100–400ms，300ms 足够合并一串事件，
   * 又短到用户感知不到延迟。
   */
  const scheduleMultiProjectSessionReload = useCallback(() => {
    if (multiProjectReloadTimerRef.current !== null) {
      window.clearTimeout(multiProjectReloadTimerRef.current);
    }
    multiProjectReloadTimerRef.current = window.setTimeout(() => {
      multiProjectReloadTimerRef.current = null;
      void loadMultiProjectSessionGroups();
    }, MULTI_PROJECT_RELOAD_DEBOUNCE_MS);
  }, [loadMultiProjectSessionGroups]);

  // 卸载时清掉挂着的定时器：否则切走页面后还会打一次扇出
  useEffect(
    () => () => {
      if (multiProjectReloadTimerRef.current !== null) {
        window.clearTimeout(multiProjectReloadTimerRef.current);
        multiProjectReloadTimerRef.current = null;
      }
    },
    [],
  );


  const { refreshMultiProjectReplyingSessions } = useReplyingSessionPoll({
    applyPendingToMultiProjectGroups,
    currentRootNodeIdRef,
    loadMultiProjectSessionGroups,
    managedRootByKeyRef,
    multiProjectPendingRef,
    multiProjectAwaitingRef,
    multiProjectSessionsEnabled,
    setMultiProjectPendingByKey,
    setMultiProjectAwaitingByKey,
    setMultiProjectSessionGroups,
  });



  return { scheduleMultiProjectSessionReload, refreshMultiProjectReplyingSessions };
}
