import { useCallback, useEffect, useRef, useState } from "react";
import { sessionService } from "../services/session";
import { reportError } from "../services/error";
import type { SessionItem } from "./appSession";
import { mergeSessionItems } from "../services/sessionListMerge";
import { useI18n } from "../i18n";

/**
 * 外部会话导入模式：选中 agent → 列表（分页/过滤）→ 勾选 → 批量导入。
 *
 * 导入成功后本地会话列表要重拉一份，但那是会话列表域的事，这里只回调 onImported 让它自己做。
 */
export function useExternalSessionImport({
  currentRootIdRef,
  getNodeIdForRoot,
  sessionListMode,
  setSessionListMode,
  isMobile,
  onSelectSession,
  onImported,
  closeRightSidebar,
}: {
  currentRootIdRef: React.MutableRefObject<string | null>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  sessionListMode: "local" | "import";
  setSessionListMode: (mode: "local" | "import") => void;
  isMobile: boolean;
  onSelectSession: (session: any, options?: { preserveTaskSelection?: boolean; preserveMainView?: boolean }) => Promise<void>;
  onImported: (rootID: string) => Promise<void>;
  closeRightSidebar: () => void;
}) {
  const { t } = useI18n();
  const [externalSessions, setExternalSessions] = useState<SessionItem[]>([]);
  const externalSessionsRef = useRef<SessionItem[]>([]);
  const [hasMoreExternalSessions, setHasMoreExternalSessions] = useState(false);
  const [loadingOlderExternalSessions, setLoadingOlderExternalSessions] = useState(false);
  const [loadingExternalSessions, setLoadingExternalSessions] = useState(false);
  const [externalSessionsError, setExternalSessionsError] = useState("");
  const [externalSelectedKey, setExternalSelectedKey] = useState("");
  const [externalImportAgent, setExternalImportAgent] = useState("");
  const externalImportAgentRef = useRef("");
  const [externalFilterBound, setExternalFilterBound] = useState(true);
  const [selectedExternalImportKeys, setSelectedExternalImportKeys] = useState<Set<string>>(() => new Set());
  const [importingExternalSessionKeys, setImportingExternalSessionKeys] = useState<Set<string>>(() => new Set());
  const [confirmingExternalImport, setConfirmingExternalImport] = useState(false);
  const [importMenuOpen, setImportMenuOpen] = useState(false);
  const importMenuRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    externalSessionsRef.current = externalSessions;
  }, [externalSessions]);
  useEffect(() => {
    externalImportAgentRef.current = externalImportAgent;
  }, [externalImportAgent]);

  useEffect(() => {
    if (!importMenuOpen) return;
    const handlePointerDown = (event: MouseEvent) => {
      if (!importMenuRef.current?.contains(event.target as Node)) {
        setImportMenuOpen(false);
      }
    };
    document.addEventListener("mousedown", handlePointerDown);
    return () => document.removeEventListener("mousedown", handlePointerDown);
  }, [importMenuOpen]);

  const loadExternalSessions = useCallback(
    async (
      rootID: string,
      agent: string,
      options?: { beforeTime?: string; afterTime?: string; replace?: boolean },
    ) => {
      if (!rootID || !agent) {
        setExternalSessions([]);
        setHasMoreExternalSessions(false);
        setExternalSessionsError("");
        return;
      }
      try {
        if (!options?.beforeTime) {
          setLoadingExternalSessions(true);
        }
        const next = (await sessionService.fetchExternalSessions(
          rootID,
          agent,
          {
            beforeTime: options?.beforeTime,
            afterTime: options?.afterTime,
            filterBound: externalFilterBound,
            limit: 50,
            nodeId: getNodeIdForRoot(rootID),
          },
        )) as SessionItem[];
        setExternalSessionsError("");
        setHasMoreExternalSessions(next.length >= 50);
        if (options?.replace || (!options?.beforeTime && !options?.afterTime)) {
          setExternalSessions(next);
          return;
        }
        setExternalSessions((prev) => mergeSessionItems(prev, next));
      } catch (err) {
        const message =
          err instanceof Error ? err.message : String(err || t("session.importLoadFailed"));
        setExternalSessionsError(message || t("session.importLoadFailed"));
        if (options?.replace || (!options?.beforeTime && !options?.afterTime)) {
          setExternalSessions([]);
          setHasMoreExternalSessions(false);
        }
        console.error("[Session] Failed to fetch external sessions:", err);
      } finally {
        setLoadingExternalSessions(false);
      }
    },
    [externalFilterBound, getNodeIdForRoot, t],
  );

  const exitImportMode = useCallback(() => {
    setSessionListMode("local");
    setExternalSelectedKey("");
    setExternalSessionsError("");
    setSelectedExternalImportKeys(new Set());
    setImportingExternalSessionKeys(new Set());
    setImportMenuOpen(false);
  }, [setSessionListMode]);

  const enterImportMode = useCallback(
    async (agentName: string) => {
      const rootID = currentRootIdRef.current || "";
      const trimmedAgent = String(agentName || "").trim();
      if (!rootID || !trimmedAgent) {
        return;
      }
      setExternalImportAgent(trimmedAgent);
      setExternalSelectedKey("");
      setExternalSessionsError("");
      setSelectedExternalImportKeys(new Set());
      setImportingExternalSessionKeys(new Set());
      setSessionListMode("import");
      await loadExternalSessions(rootID, trimmedAgent, { replace: true });
    },
    [currentRootIdRef, loadExternalSessions, setSessionListMode],
  );

  const handleLoadOlderExternalSessions = useCallback(async () => {
    const rootID = currentRootIdRef.current || "";
    const oldest =
      externalSessionsRef.current[externalSessionsRef.current.length - 1]?.updated_at || "";
    if (!rootID || !externalImportAgent || !oldest || loadingOlderExternalSessions) {
      return;
    }
    setLoadingOlderExternalSessions(true);
    try {
      await loadExternalSessions(rootID, externalImportAgent, { beforeTime: oldest });
    } finally {
      setLoadingOlderExternalSessions(false);
    }
  }, [currentRootIdRef, externalImportAgent, loadExternalSessions, loadingOlderExternalSessions]);

  const toggleExternalImportSelection = useCallback((session: SessionItem) => {
    const sessionKey = String(session.agent_session_id || session.key || "").trim();
    if (!sessionKey) {
      return;
    }
    setSelectedExternalImportKeys((current) => {
      const next = new Set(current);
      if (next.has(sessionKey)) {
        next.delete(sessionKey);
      } else {
        next.add(sessionKey);
      }
      return next;
    });
  }, []);

  const toggleAllExternalImportSelection = useCallback((checked: boolean) => {
    const visibleKeys = externalSessionsRef.current
      .map((session) => String(session.agent_session_id || session.key || "").trim())
      .filter(Boolean);
    setSelectedExternalImportKeys((current) => {
      const next = new Set(current);
      visibleKeys.forEach((key) => {
        if (checked) {
          next.add(key);
        } else {
          next.delete(key);
        }
      });
      return next;
    });
  }, []);

  const handleConfirmExternalImport = useCallback(async () => {
    const rootID = currentRootIdRef.current || "";
    const sessionKeys = [...selectedExternalImportKeys].filter(Boolean);
    if (!rootID || !externalImportAgent || !sessionKeys.length || confirmingExternalImport) {
      return;
    }
    setConfirmingExternalImport(true);
    setImportingExternalSessionKeys(new Set(sessionKeys));
    try {
      const imported = await sessionService.importExternalSessionsBatch(
        rootID,
        externalImportAgent,
        sessionKeys,
      );
      const results = imported?.items || [];
      const successItems = results.filter((item) => item.success && item.session_key);
      if (!imported || !successItems.length) {
        const firstError = results.find((item) => !item.success)?.error;
        reportError(
          "session.import_failed",
          firstError ? t("session.importFailedWithReason", { reason: firstError }) : t("session.importFailed"),
        );
        return;
      }
      setConfirmingExternalImport(false);
      setImportingExternalSessionKeys(new Set());
      const failedKeys = new Set(
        results
          .filter((item) => !item.success)
          .map((item) => String(item.agent_session_id || "").trim())
          .filter(Boolean),
      );
      if (failedKeys.size > 0) {
        const firstError = results.find((item) => !item.success)?.error;
        const message = firstError
          ? t("session.importPartialFailedWithReason", { count: failedKeys.size, reason: firstError })
          : t("session.importPartialFailed", { count: failedKeys.size });
        reportError("session.import_failed", message);
      }
      setSelectedExternalImportKeys(failedKeys);
      if (failedKeys.size === 0) {
        exitImportMode();
      }
      await onImported(rootID);
      const firstImported = successItems[0];
      if (firstImported?.session_key) {
        const source = externalSessionsRef.current.find(
          (item) =>
            String(item.agent_session_id || item.key || "").trim() ===
            String(firstImported.agent_session_id || "").trim(),
        );
        await onSelectSession({
          key: firstImported.session_key,
          root_id: rootID,
          type: "chat",
          agent: externalImportAgent,
          name: source?.name,
        } as SessionItem);
      }
      if (isMobile && failedKeys.size === 0) {
        closeRightSidebar();
      }
    } finally {
      setConfirmingExternalImport(false);
      setImportingExternalSessionKeys(new Set());
    }
  }, [
    closeRightSidebar,
    confirmingExternalImport,
    currentRootIdRef,
    exitImportMode,
    externalImportAgent,
    isMobile,
    onImported,
    onSelectSession,
    selectedExternalImportKeys,
    t,
  ]);

  // 导入模式下的过滤开关 / agent 变化 → 重拉第一页
  useEffect(() => {
    if (sessionListMode !== "import") return;
    const rootID = currentRootIdRef.current || "";
    if (!rootID || !externalImportAgent) return;
    setSelectedExternalImportKeys(new Set());
    void loadExternalSessions(rootID, externalImportAgent, { replace: true });
  }, [sessionListMode, externalImportAgent, externalFilterBound, loadExternalSessions, currentRootIdRef]);

  /**
   * WS 推来「某条外部会话已导入完成」：清掉它在导入中/已勾选里的痕迹并重拉列表。
   * 导入模式下才算数，且 agent 对不上就忽略（别的 agent 的推送）。
   */
  const handleImportedSessionConfirmed = useCallback(
    (agentSessionID: string, agentName: string) => {
      if (sessionListMode !== "import" || !agentSessionID) {
        return;
      }
      if (agentName && agentName !== externalImportAgentRef.current) {
        return;
      }
      setImportingExternalSessionKeys((current) => {
        if (!current.has(agentSessionID)) return current;
        const next = new Set(current);
        next.delete(agentSessionID);
        return next;
      });
      setSelectedExternalImportKeys((current) => {
        if (!current.has(agentSessionID)) return current;
        const next = new Set(current);
        next.delete(agentSessionID);
        return next;
      });
      setConfirmingExternalImport(false);
      const rootID = currentRootIdRef.current || "";
      if (externalImportAgentRef.current && rootID) {
        void loadExternalSessions(rootID, externalImportAgentRef.current, { replace: true });
      }
    },
    [currentRootIdRef, loadExternalSessions, sessionListMode],
  );

  return {
    externalSessions,
    setExternalSessions,
    externalSessionsRef,
    hasMoreExternalSessions,
    loadingOlderExternalSessions,
    loadingExternalSessions,
    externalSessionsError,
    externalSelectedKey,
    setExternalSelectedKey,
    externalImportAgent,
    setExternalImportAgent,
    externalImportAgentRef,
    externalFilterBound,
    setExternalFilterBound,
    selectedExternalImportKeys,
    setSelectedExternalImportKeys,
    importingExternalSessionKeys,
    setImportingExternalSessionKeys,
    confirmingExternalImport,
    setConfirmingExternalImport,
    importMenuOpen,
    setImportMenuOpen,
    importMenuRef,
    loadExternalSessions,
    enterImportMode,
    exitImportMode,
    handleLoadOlderExternalSessions,
    toggleExternalImportSelection,
    toggleAllExternalImportSelection,
    handleConfirmExternalImport,
    handleImportedSessionConfirmed,
  };
}

