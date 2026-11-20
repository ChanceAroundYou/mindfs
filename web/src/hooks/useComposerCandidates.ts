import { useEffect, useRef, useState } from "react";
import { fetchCandidates, type CandidateItem } from "../services/candidates";
import { CANDIDATE_FETCH_DEBOUNCE_MS } from "../shared/actionBarUtils";
import type { ActiveToken } from "../components/editor/tokenEditorUtils";

type UseComposerCandidatesParams = {
  rootId: string | null | undefined;
  nodeId?: string;
  /** / 命令候选需要的 agent；任务侧固定用默认 agent。 */
  agent?: string;
  activeToken: ActiveToken | null;
};

/**
 * 任务侧输入框（新建任务 / 任务详情）的 @ 文件、# 提示词、/ 命令候选。
 *
 * 拉取规则与 `useTaskInlineCandidates` 对齐：file→file、prompt→prompt、
 * slash→skill（带 agent）、command→command；视图层只拿 candidates 和
 * 高亮索引，不碰 services。
 */
export function useComposerCandidates({
  rootId,
  nodeId,
  agent,
  activeToken,
}: UseComposerCandidatesParams) {
  const [candidates, setCandidates] = useState<CandidateItem[]>([]);
  const [activeCandidateIndex, setActiveCandidateIndex] = useState(0);
  const candidateItemRefs = useRef<Array<HTMLButtonElement | null>>([]);

  useEffect(() => {
    if (!activeToken || !rootId) {
      setCandidates([]);
      setActiveCandidateIndex(0);
      return;
    }
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      fetchCandidates({
        rootId,
        nodeId,
        type: activeToken.type === "file"
          ? "file"
          : activeToken.type === "prompt"
            ? "prompt"
            : activeToken.type === "command"
              ? "command"
              : "skill",
        query: activeToken.query,
        agent: activeToken.type === "slash" ? agent : undefined,
        signal: controller.signal,
      })
        .then((items) => {
          setCandidates(items);
          setActiveCandidateIndex(0);
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
  }, [rootId, nodeId, agent, activeToken]);

  useEffect(() => {
    if (candidates.length === 0) {
      candidateItemRefs.current = [];
      return;
    }
    if (activeCandidateIndex < 0) return;
    candidateItemRefs.current[activeCandidateIndex]?.scrollIntoView({ block: "nearest" });
  }, [candidates, activeCandidateIndex]);

  return {
    candidates,
    setCandidates,
    activeCandidateIndex,
    setActiveCandidateIndex,
    candidateItemRefs,
  };
}
