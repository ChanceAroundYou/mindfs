import { useEffect, useState } from "react";
import { fetchTaskCompletion } from "../../services/task/taskCompletion";
import type { TaskCompletionView } from "../../shared/taskCompletion";

// 完成证据清单的取数逻辑（.ts，允许 import services）。
//
// 挂载即拉一次：清单有没有内容决定入口显不显示，等展开再拉就永远显示不出来。
// 任务头每次更新（refreshKey）重拉，跟运行视图同一套节奏。请求失败按「没有记录」
// 处理，不打扰用户。
export type TaskCompletionState = {
  completion: TaskCompletionView | null;
  loaded: boolean;
  loading: boolean;
  error: string;
};

export function useTaskCompletion(
  rootId: string,
  taskId: string,
  nodeId?: string,
  refreshKey?: string,
): TaskCompletionState {
  const [completion, setCompletion] = useState<TaskCompletionView | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    setCompletion(null);
    setLoaded(false);
    setError("");
  }, [rootId, taskId, nodeId]);

  useEffect(() => {
    if (!rootId || !taskId) return undefined;
    let cancelled = false;
    setLoading(true);
    fetchTaskCompletion(rootId, taskId, nodeId)
      .then((view) => {
        if (cancelled) return;
        setCompletion(view);
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

  return { completion, loaded, loading, error };
}
