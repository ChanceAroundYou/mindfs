import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { fetchRouterDecisions } from "../../services/task/routerDecisions";
import type { RouterDecisionSummary } from "../../shared/routerDecisions";
import { supervisionRows, type SupervisionRow } from "./supervisionEntryModel";

// 监管记录入口的取数逻辑（.ts，允许 import services）。
//
// 挂载即拉一次：入口本身要不要显示取决于「有没有监管决策」，光靠展开时拉就
// 永远显示不出来。任务头每次更新（refreshKey）重拉，跟运行视图同一套节奏。
// 请求失败不打扰用户：入口按「没有记录」处理，错误只在已展开且已有行时展示。
export type SupervisionEntryState = {
  rows: SupervisionRow[];
  /** 至少成功拉到过一次（失败与「还没拉」都不会让入口冒出来）。 */
  loaded: boolean;
  open: boolean;
  loading: boolean;
  error: string;
  toggle: () => void;
};

export function useSupervisionEntry(
  rootId: string,
  taskId: string,
  nodeId?: string,
  refreshKey?: string,
): SupervisionEntryState {
  const [decisions, setDecisions] = useState<RouterDecisionSummary[] | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const openRef = useRef(false);
  const identityRef = useRef("");

  useEffect(() => {
    const identity = `${rootId}|${taskId}|${nodeId || ""}`;
    if (identityRef.current !== identity) {
      identityRef.current = identity;
      setDecisions(null);
      setLoaded(false);
      setOpen(false);
      openRef.current = false;
      setError("");
    }
  }, [rootId, taskId, nodeId]);

  useEffect(() => {
    if (!rootId || !taskId) return undefined;
    let cancelled = false;
    setLoading(true);
    fetchRouterDecisions(rootId, taskId, nodeId)
      .then((list) => {
        if (cancelled) return;
        setDecisions(list);
        setLoaded(true);
        setError("");
      })
      .catch((err) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [rootId, taskId, nodeId, refreshKey]);

  const rows = useMemo(() => supervisionRows(decisions), [decisions]);

  const toggle = useCallback(() => {
    openRef.current = !openRef.current;
    setOpen(openRef.current);
  }, []);

  return { rows, loaded, open, loading, error, toggle };
}
