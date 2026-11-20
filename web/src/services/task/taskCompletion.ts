import { appURL } from "../net/base";
import { protectedJSON } from "../net/api";
import type { TaskCompletionView } from "../../shared/taskCompletion";

// 任务完成证据清单（只读）。null = 这个任务从来没有完成记录（区别于「有记录但
// 一条都不通过」——那时 items 非空、passed=false）。
export async function fetchTaskCompletion(
  rootId: string,
  taskId: string,
  nodeId?: string,
): Promise<TaskCompletionView | null> {
  const params = new URLSearchParams({ root_id: rootId });
  const payload = await protectedJSON<{ completion?: TaskCompletionView | null }>(
    appURL(`/api/tasks/${encodeURIComponent(taskId)}/completion`, params, nodeId),
  );
  return payload && payload.completion ? payload.completion : null;
}
