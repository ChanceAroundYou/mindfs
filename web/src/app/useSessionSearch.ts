import { useCallback, useEffect, useState } from "react";
import { sessionService } from "../services/session";
import { toSessionItem, type SessionItem } from "./appSession";

/**
 * 会话搜索：查询串 → 应用串 → 结果。`applied` 与 `query` 分开是为了「回车才搜」。
 *
 * 只在本地列表模式（非导入模式）且面板打开时发请求；切项目/切模式一律清空。
 */
export function useSessionSearch({
  currentRootId,
  sessionListMode,
  multiProjectSessionsEnabled,
  getNodeIdForRoot,
}: {
  currentRootId: string | null;
  sessionListMode: "local" | "import";
  multiProjectSessionsEnabled: boolean;
  getNodeIdForRoot: (rootId: string) => string | undefined;
}) {
  const [sessionSearchOpen, setSessionSearchOpen] = useState(false);
  const [sessionSearchResultsMode, setSessionSearchResultsMode] = useState(false);
  const [sessionSearchQuery, setSessionSearchQuery] = useState("");
  const [sessionSearchAppliedQuery, setSessionSearchAppliedQuery] = useState("");
  const [sessionSearchResults, setSessionSearchResults] = useState<SessionItem[]>([]);
  const [sessionSearchLoading, setSessionSearchLoading] = useState(false);

  const resetSessionSearch = useCallback(() => {
    setSessionSearchResultsMode(false);
    setSessionSearchQuery("");
    setSessionSearchAppliedQuery("");
    setSessionSearchResults([]);
    setSessionSearchLoading(false);
  }, []);

  const closeSessionSearch = useCallback(() => {
    setSessionSearchOpen(false);
    resetSessionSearch();
  }, [resetSessionSearch]);

  const openSessionSearch = useCallback(() => {
    setSessionSearchOpen(true);
    resetSessionSearch();
  }, [resetSessionSearch]);

  const toggleSessionSearch = useCallback(() => {
    setSessionSearchOpen((prev) => {
      if (prev) {
        resetSessionSearch();
      }
      return !prev;
    });
  }, [resetSessionSearch]);

  const executeSessionSearch = useCallback(() => {
    const trimmed = sessionSearchQuery.trim();
    if (trimmed.length < 2) {
      return;
    }
    setSessionSearchResultsMode(true);
    setSessionSearchAppliedQuery(trimmed);
  }, [sessionSearchQuery]);

  useEffect(() => {
    if (sessionListMode !== "local") {
      closeSessionSearch();
    }
  }, [sessionListMode, closeSessionSearch]);

  useEffect(() => {
    resetSessionSearch();
  }, [currentRootId, resetSessionSearch]);

  useEffect(() => {
    if (
      sessionListMode !== "local" ||
      !sessionSearchOpen ||
      !currentRootId ||
      !sessionSearchAppliedQuery
    ) {
      setSessionSearchLoading(false);
      return;
    }

    let cancelled = false;
    const searchAcrossRoots = multiProjectSessionsEnabled;
    setSessionSearchLoading(true);
    void sessionService
      .searchSessions(currentRootId, sessionSearchAppliedQuery, 20, {
        multiRoot: searchAcrossRoots,
        nodeId: searchAcrossRoots ? undefined : getNodeIdForRoot(currentRootId),
      })
      .then((hits) => {
        if (cancelled) return;
        const mapped = hits
          .map((hit) => {
            const hitRootId = String(hit.root_id || currentRootId || "");
            const item = toSessionItem(hitRootId, {
              ...hit,
              root_id: hitRootId,
              search_seq: hit.seq,
              search_snippet: hit.snippet,
              search_match_type: hit.match_type,
            });
            return item;
          })
          .filter((item): item is SessionItem => !!item);
        setSessionSearchResults(mapped);
      })
      .catch((err) => {
        if (cancelled) return;
        console.error("[session.search] failed", {
          rootId: currentRootId,
          query: sessionSearchAppliedQuery,
          err,
        });
        setSessionSearchResults([]);
      })
      .finally(() => {
        if (!cancelled) {
          setSessionSearchLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [currentRootId, getNodeIdForRoot, multiProjectSessionsEnabled, sessionListMode, sessionSearchAppliedQuery, sessionSearchOpen]);

  const handleSearchQueryChange = useCallback((value: string) => {
    setSessionSearchQuery(value);
    if (!value.trim() && !sessionSearchResultsMode) {
      setSessionSearchAppliedQuery("");
      setSessionSearchResults([]);
      setSessionSearchLoading(false);
    }
  }, [sessionSearchResultsMode]);

  return {
    sessionSearchOpen,
    setSessionSearchOpen,
    sessionSearchResultsMode,
    setSessionSearchResultsMode,
    sessionSearchQuery,
    setSessionSearchQuery,
    sessionSearchAppliedQuery,
    setSessionSearchAppliedQuery,
    sessionSearchResults,
    setSessionSearchResults,
    sessionSearchLoading,
    setSessionSearchLoading,
    executeSessionSearch,
    openSessionSearch,
    closeSessionSearch,
    toggleSessionSearch,
    handleSearchQueryChange,
    resetSessionSearch,
  };
}
