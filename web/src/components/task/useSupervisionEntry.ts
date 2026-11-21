import { useEffect, useMemo, useRef, useState } from "react";
import { fetchRouterDecisions } from "../../services/task/routerDecisions";
import type { RouterDecisionSummary } from "../../shared/routerDecisions";
import { supervisionRows, type SupervisionRow } from "./supervisionEntryModel";

// 监管记录面板的取数逻辑（.ts，允许 import services）。
//
// 挂载即拉一次：面板与执行详情共用一行胶囊，按钮始终存在，数据要提前就位，
// 光靠展开时拉会让每次展开都空一拍。任务头每次更新（refreshKey）重拉，跟
// 运行视图同一套节奏。请求失败不打扰用户：面板只展示错误，不影响胶囊。
export type SupervisionEntryState = {
  rows: SupervisionRow[];
  /** 至少成功拉到过一次。 */
  loaded: boolean;
  loading: boolean;
  error: string;
};

export function useSupervisionEntry(
  rootId: string,
  taskId: string,
  nodeId?: string,
  refreshKey?: string,
): SupervisionEntryState {
  const [decisions, setDecisions] = useState<RouterDecisionSummary[] | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const identityRef = useRef("");

  useEffect(() => {
    const identity = `${rootId}|${taskId}|${nodeId || ""}`;
    if (identityRef.current !== identity) {
      identityRef.current = identity;
      setDecisions(null);
      setLoaded(false);
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

  return { rows, loaded, loading, error };
}
