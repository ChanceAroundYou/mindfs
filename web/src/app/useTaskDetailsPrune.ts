/**
 * 任务详情的「内存淘汰」—— 从 useTaskBoard 抽出（2026-11-20）。
 *
 * `taskDetailsById` 是跨项目共享的一张表，权威集合只覆盖本次拉取的那个项目，
 * 所以淘汰必须同时按**项目身份**（nodeId + rootId）圈定：`root_id` 只是一台机器
 * 内部的 id，fn 与 pc 都有 `docs`，只按 root_id 判会把另一台机器的同名项目一起
 * 清空（卡片退化成冻结快照，还会被当成「服务端已删除」）。
 */
import { useCallback, type MutableRefObject, type Dispatch, type SetStateAction } from "react";
import type { KanbanTask, TaskDetail } from "../services/tasks";

export type UseTaskDetailsPruneParams = {
  taskDetailsByIdRef: MutableRefObject<Record<string, TaskDetail>>;
  setTaskDetailsById: Dispatch<SetStateAction<Record<string, TaskDetail>>>;
  setTaskFirstInputById: Dispatch<SetStateAction<Record<string, string>>>;
  setTaskSessionKeysById: Dispatch<SetStateAction<Record<string, string[]>>>;
  setKanbanTasks: Dispatch<SetStateAction<KanbanTask[]>>;
  setKanbanTaskCountItems: Dispatch<SetStateAction<KanbanTask[]>>;
};

/**
 * 把「服务端全量响应里已经没有」的任务从内存里摘掉。
 *
 * 缓存以前只写不删，内存这边同样只有合并（applyTaskDetails）没有移除，于是
 * 服务端删掉的任务会被一直渲染。必须真的 delete 键：kanbanTasks 和
 * kanbanTaskCountItems 都是从 taskDetailsById 派生的，只过滤数组不改 map 的话
 * 下一轮派生又会把它们带回来。
 */
export function useTaskDetailsPrune(params: UseTaskDetailsPruneParams) {
  const {
    taskDetailsByIdRef, setTaskDetailsById, setTaskFirstInputById,
    setTaskSessionKeysById, setKanbanTasks, setKanbanTaskCountItems,
  } = params;

  return useCallback((rootId: string, keepTaskIds: Iterable<string>, nodeId?: string) => {
    const keep = new Set(Array.from(keepTaskIds, (id) => String(id || "")).filter(Boolean));
    const nid = String(nodeId || "").trim();
    const inScope = (task: KanbanTask) => {
      if (task.root_id !== rootId) return false;
      if (!nid) return true;
      const taskNid = String((task as any)?._nodeId || "").trim();
      return !taskNid || taskNid === nid;
    };
    const dropped = Object.entries(taskDetailsByIdRef.current)
      .filter(([taskId, detail]) => !keep.has(String(taskId)) && inScope((detail as TaskDetail).task))
      .map(([taskId]) => taskId);
    if (dropped.length === 0) return;
    const droppedSet = new Set(dropped);
    const kept = <V,>(entries: [string, V][]) =>
      Object.fromEntries(entries.filter(([taskId]) => !droppedSet.has(taskId))) as Record<string, V>;
    taskDetailsByIdRef.current = kept(Object.entries(taskDetailsByIdRef.current));
    setTaskDetailsById((prev) => kept(Object.entries(prev)));
    // 这两张表只有 taskId → 值，没有 root 信息可判归属，所以直接吃上面算好的
    // 淘汰名单：跟着 taskDetailsById 一起摘掉，不自己再推导一遍。
    setTaskFirstInputById((prev) => kept(Object.entries(prev)));
    setTaskSessionKeysById((prev) => kept(Object.entries(prev)));
    setKanbanTasks((prev) => {
      const next = prev.filter((task) => !inScope(task) || keep.has(String(task.id)));
      return next.length === prev.length ? prev : next;
    });
    setKanbanTaskCountItems((prev) => {
      const next = prev.filter((task) => !inScope(task) || keep.has(String(task.id)));
      return next.length === prev.length ? prev : next;
    });
  }, [taskDetailsByIdRef, setTaskDetailsById, setTaskFirstInputById, setTaskSessionKeysById, setKanbanTasks, setKanbanTaskCountItems]);
}
