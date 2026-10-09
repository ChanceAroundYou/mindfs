import { useCallback } from "react";

import type {
  MultiProjectSessionGroup,
  SessionItem,
  SlashCommandResult,
} from "./appSession";

export type UseAppSessionKeyStateParams = {
  rootSessionKey: (rootId: string, sessionKey: string) => string;
  setSlashCommandResults: React.Dispatch<
    React.SetStateAction<Record<string, SlashCommandResult>>
  >;
  setMultiProjectPendingByKey: React.Dispatch<
    React.SetStateAction<Record<string, boolean>>
  >;
  multiProjectPendingRef: React.RefObject<Record<string, boolean>>;
  setMultiProjectSessionGroups: React.Dispatch<
    React.SetStateAction<MultiProjectSessionGroup[]>
  >;
  applyPendingToMultiProjectGroups: (
    groups: MultiProjectSessionGroup[],
    pendingByKey: Record<string, boolean>,
  ) => {
    sessions: SessionItem[];
    rootId: string;
    rootName: string;
    latestSessionTime: string;
    totalCount: number;
    _nodeId?: string;
    _nodeColor?: string;
    _nodeName?: string;
    _unreachable?: boolean;
  }[];
};

/**
 * 按「项目 + 会话」这个复合键存的两份派生状态：斜杠命令结果、跨项目回复中。
 *
 * 两者都只认键、不认对象引用 —— 会话对象每次拉列表都是新的，拿对象当 key 永远匹配不上。
 * 所以合成一个 hook：它们共享同一个 rootSessionKey 和同一套「按键增删」的写法。
 */
export function useAppSessionKeyState(params: UseAppSessionKeyStateParams) {
  const {
    rootSessionKey,
    setSlashCommandResults,
    setMultiProjectPendingByKey,
    multiProjectPendingRef,
    setMultiProjectSessionGroups,
    applyPendingToMultiProjectGroups,
  } = params;

  const clearSlashCommandResultForSession = useCallback(
    (rootID: string, sessionKey: string) => {
      const key = rootSessionKey(rootID, sessionKey);
      setSlashCommandResults((prev) => {
        if (!prev[key]) {
          return prev;
        }
        const next = { ...prev };
        delete next[key];
        return next;
      });
    },
    [rootSessionKey],
  );

  const setMultiProjectSessionPending = useCallback(
    (rootID: string | null | undefined, sessionKey: string | null | undefined, pending: boolean) => {
      const resolvedRoot = String(rootID || "");
      const resolvedKey = String(sessionKey || "");
      if (!resolvedRoot || !resolvedKey) {
        return;
      }
      const key = rootSessionKey(resolvedRoot, resolvedKey);
      setMultiProjectPendingByKey((prev) => {
        const next = { ...prev };
        if (pending) {
          next[key] = true;
        } else {
          delete next[key];
        }
        multiProjectPendingRef.current = next;
        setMultiProjectSessionGroups((groups) => applyPendingToMultiProjectGroups(groups, next));
        return next;
      });
    },
    [applyPendingToMultiProjectGroups, rootSessionKey],
  );

  return { clearSlashCommandResultForSession, setMultiProjectSessionPending };
}