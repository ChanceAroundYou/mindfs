import { useEffect, useRef, useState } from "react";
import { fetchCandidates, type CandidateItem } from "../../services/candidates";
import { CANDIDATE_FETCH_DEBOUNCE_MS } from "./styleHelpers";
import { type ActiveToken } from "../editor/tokenEditorUtils";

/** 候选下拉：按 activeToken 防抖拉取 + 键盘高亮态 + 自动滚到可见。 */
export function useCandidates({
  activeToken,
  currentRootId,
  agent,
  mode,
  isFocused,
  clearActiveToken,
}: {
  activeToken: { type: "file" | "slash" | "prompt" | "command"; query: string } | null;
  currentRootId?: string | null;
  agent: string;
  mode: string;
  isFocused: boolean;
  clearActiveToken: () => void;
}) {
  const [candidates, setCandidates] = useState<CandidateItem[]>([]);
  const [activeCandidateIndex, setActiveCandidateIndex] = useState(0);
  const [promptCandidateRefresh, setPromptCandidateRefresh] = useState(0);
  const candidateAbortRef = useRef<AbortController | null>(null);
  const candidateItemRefs = useRef<Array<HTMLDivElement | null>>([]);

  useEffect(() => () => candidateAbortRef.current?.abort(), []);

  useEffect(() => {
    if (mode !== "command") {
      return;
    }
    if (!isFocused) {
      candidateAbortRef.current?.abort();
      clearActiveToken();
      setCandidates([]);
      setActiveCandidateIndex(0);
    }
  }, [mode, isFocused, clearActiveToken]);

  useEffect(() => {
    if (!activeToken || !currentRootId || (activeToken.type === "slash" && mode !== "command" && !agent)) {
      candidateAbortRef.current?.abort();
      setCandidates([]);
      setActiveCandidateIndex(0);
      return;
    }
    const controller = new AbortController();
    candidateAbortRef.current?.abort();
    candidateAbortRef.current = controller;
    const timer = window.setTimeout(() => {
      fetchCandidates({
        rootId: currentRootId,
        type: activeToken.type === "file"
          ? "file"
          : activeToken.type === "prompt"
            ? "prompt"
            : activeToken.type === "command" || mode === "command"
              ? "command"
              : "skill",
        query: activeToken.query,
        agent: activeToken.type === "slash" && mode !== "command" ? agent : undefined,
        signal: controller.signal,
      })
        .then((items) => {
          const supportsPlanCommand =
            activeToken.type !== "slash" ||
            mode === "command" ||
            ["codex", "claude"].includes(agent.trim().toLowerCase());
          const filteredItems = supportsPlanCommand
            ? items
            : items.filter(
                (item) =>
                  item.type !== "slash_command" ||
                  item.name.trim().toLowerCase() !== "plan",
              );
          const nextItems = activeToken.type === "command"
            ? filteredItems.filter((item) => item.name.trim() !== activeToken.query.trim())
            : filteredItems;
          setCandidates(nextItems);
          setActiveCandidateIndex(activeToken.type === "command" ? -1 : 0);
        })
        .catch((err) => {
          if (controller.signal.aborted) return;
          console.error("Failed to fetch candidates:", err);
          setCandidates([]);
          setActiveCandidateIndex(0);
        });
    }, CANDIDATE_FETCH_DEBOUNCE_MS);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [activeToken, currentRootId, agent, mode, promptCandidateRefresh]);

  useEffect(() => {
    if (candidates.length === 0) {
      candidateItemRefs.current = [];
      return;
    }
    if (activeCandidateIndex < 0) {
      return;
    }
    const activeItem = candidateItemRefs.current[activeCandidateIndex];
    if (!activeItem) {
      return;
    }
    activeItem.scrollIntoView({ block: "nearest" });
  }, [candidates, activeCandidateIndex]);

  return {
    candidates,
    setCandidates,
    activeCandidateIndex,
    setActiveCandidateIndex,
    candidateItemRefs,
    bumpPromptCandidateRefresh: () => setPromptCandidateRefresh((value) => value + 1),
  };
}
