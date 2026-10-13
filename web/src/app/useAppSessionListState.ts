import { useRef, useState } from "react";
import { useAppSessionPins } from "./useAppSessionPins";
import type { MultiProjectSessionGroup, SessionItem } from "./appSession";

/** 会话列表、跨项目 pending 和归档面板的状态集合。 */
export function useAppSessionListState() {
  const [sessions, setSessions] = useState<SessionItem[]>([]);
  const sessionsRef = useRef<SessionItem[]>([]);
  // 下游签名依赖字面量类型，不能拓宽为 boolean / number。
  const multiProjectSessionsEnabled: true = true;
  // session.created 的多项目重拉在 300ms 窗口内合并。
  const MULTI_PROJECT_RELOAD_DEBOUNCE_MS: 300 = 300;
  const [multiProjectSessionGroups, setMultiProjectSessionGroups] = useState<MultiProjectSessionGroup[]>([]);
  const multiProjectSessionGroupsRef = useRef<MultiProjectSessionGroup[]>([]);
  const [multiProjectSessionsLoading, setMultiProjectSessionsLoading] = useState(false);
  const multiProjectLoadSeqRef = useRef(0);
  const [multiProjectPendingByKey, setMultiProjectPendingByKey] = useState<Record<string, boolean>>({});
  const multiProjectPendingRef = useRef<Record<string, boolean>>({});
  const { applyPinSnapshot } = useAppSessionPins();
  const [syncingSessionKeys, setSyncingSessionKeys] = useState<Set<string>>(() => new Set());
  const [archivedGroups, setArchivedGroups] = useState<MultiProjectSessionGroup[]>([]);
  const [archiveOpen, setArchiveOpen] = useState(false);
  const [archiveLoading, setArchiveLoading] = useState(false);
  const [archiveReloadToken, setArchiveReloadToken] = useState(0);
  const [hasMoreSessions, setHasMoreSessions] = useState(false);
  const [loadingOlderSessions, setLoadingOlderSessions] = useState(false);
  const [sessionListMode, setSessionListMode] = useState<"local" | "import">("local");

  return {
    sessions, setSessions, sessionsRef, multiProjectSessionsEnabled, MULTI_PROJECT_RELOAD_DEBOUNCE_MS,
    multiProjectSessionGroups, setMultiProjectSessionGroups, multiProjectSessionGroupsRef,
    multiProjectSessionsLoading, setMultiProjectSessionsLoading, multiProjectLoadSeqRef,
    multiProjectPendingByKey, setMultiProjectPendingByKey, multiProjectPendingRef, applyPinSnapshot,
    syncingSessionKeys, setSyncingSessionKeys, archivedGroups, setArchivedGroups, archiveOpen,
    setArchiveOpen, archiveLoading, setArchiveLoading, archiveReloadToken, setArchiveReloadToken,
    hasMoreSessions, setHasMoreSessions, loadingOlderSessions, setLoadingOlderSessions,
    sessionListMode, setSessionListMode,
  };
}
