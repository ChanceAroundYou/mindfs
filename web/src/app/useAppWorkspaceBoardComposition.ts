import { useCallback, useMemo, useState } from "react";
import { getActiveNode } from "../services/net/nodeRegistry";
import type { KanbanTask, TaskDetail } from "../services/tasks";
import { loadWorkspaceCollapsed, loadWorkspaceFilter, saveWorkspaceCollapsed, saveWorkspaceFilter } from "./appStorage";
import type { WorkspaceBoardFilter } from "./appStorage";
import { useWorkspaceBoard } from "./useWorkspaceBoard";
import type { UseAppViewCompositionParams } from "./useAppViewCompositionTypes";

export function useAppWorkspaceBoardComposition({
  workspaceOpen, workspaceBoardRefreshRef, managedRootIds, getRootDisplayName,
  getDisplayNodeColor, getNodeIdForRoot, currentRootIdRef, taskDetailsById,
}: Pick<UseAppViewCompositionParams,
  "workspaceOpen" | "workspaceBoardRefreshRef" | "managedRootIds" | "getRootDisplayName" |
  "getDisplayNodeColor" | "getNodeIdForRoot" | "currentRootIdRef" | "taskDetailsById"
>) {
  // 跨项目工作台：按项目聚合的跨节点任务总览（扇出与建组都在 useWorkspaceBoard 内）。
  // refreshToken 由刷新按钮驱动；WS 的高频推送靠 C1 放行的 task.updated 增量更新就地生效，
  // 不驱动整包重拉（否则多节点扇出会被事件风暴打爆）。
  const [workspaceBoardToken, setWorkspaceBoardToken] = useState(0);
  const [workspaceFilter, setWorkspaceFilter] = useState<WorkspaceBoardFilter>(loadWorkspaceFilter);
  const [workspaceCollapsedKeys, setWorkspaceCollapsedKeys] = useState<Set<string>>(loadWorkspaceCollapsed);
  const toggleWorkspaceProject = useCallback((key: string) => {
    setWorkspaceCollapsedKeys((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      saveWorkspaceCollapsed(next);
      return next;
    });
  }, []);
  const changeWorkspaceFilter = useCallback((filter: WorkspaceBoardFilter) => {
    setWorkspaceFilter(filter);
    saveWorkspaceFilter(filter);
  }, []);
  // 卡片的新鲜度取自 taskDetailsById（applyTaskDetails 的唯一落点）。
  // 这里刻意由 state 派生而**不是**每次现读 ref：工作台那两个 useMemo 靠依赖变化
  // 才重算，走 ref 的话 React 看不见任何变化 —— 整条新鲜度路径会静默失效
  // （卡片照样纹丝不动，且没有任何报错可查）。useMemo 保住引用稳定，
  // 又让它在 taskDetailsById 真变时换新身份。
  const liveTasksById = useMemo(() => {
    const out: Record<string, KanbanTask> = {};
    for (const detail of Object.values(taskDetailsById)) {
      if (detail?.task?.id) out[detail.task.id] = detail.task;
    }
    return out;
  }, [taskDetailsById]);
  const getLiveTask = useCallback((taskId: string) => liveTasksById[taskId], [liveTasksById]);
  const workspaceBoard = useWorkspaceBoard({
    enabled: workspaceOpen,
    refreshToken: workspaceBoardToken,
    managedRootIds,
    getRootDisplayName,
    getNodeColor: getDisplayNodeColor,
    getLiveTask,
    getNodeId: getNodeIdForRoot,
    fallbackNodeId: getNodeIdForRoot(String(currentRootIdRef.current || "")) || String(getActiveNode()?.id || ""),
    filter: workspaceFilter,
  });
  // 渲染时挂上去，早期调用点（卡片动作、WS 处理器）转发到它。见 workspaceBoardRefreshRef。
  workspaceBoardRefreshRef.current = workspaceBoard.refresh;
  return {
    workspaceBoard, workspaceFilter, changeWorkspaceFilter, workspaceCollapsedKeys,
    toggleWorkspaceProject, setWorkspaceBoardToken,
  };
}
