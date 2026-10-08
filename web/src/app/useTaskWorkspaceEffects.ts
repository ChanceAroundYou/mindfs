import { useEffect } from "react";

type TaskWorkspaceEffectsProps = {
  bootstrapPhase: string;
  currentRootId?: string | null;
  currentRootIdRef: { current: string | null };
  setAgentsVersion: (update: (value: number) => number) => void;
  loadTaskTemplates: () => unknown;
  loadKanbanTasks: (rootId: string | null) => unknown;
  loadMultiProjectSessionGroups: () => unknown;
  multiProjectSessionsEnabled: boolean;
};

/**
 * 任务领域的冷启动刷新副作用。
 *
 * 任务模板、Kanban 和跨项目会话都必须等 bootstrap ready 后才拉取；这条顺序
 * 是 App 初始化契约的一部分，单独收口但不合并其它 effect，避免改变依赖与时序。
 */
export function useTaskWorkspaceEffects({
  bootstrapPhase,
  currentRootIdRef,
  setAgentsVersion,
  loadTaskTemplates,
  loadKanbanTasks,
  loadMultiProjectSessionGroups,
  multiProjectSessionsEnabled,
}: TaskWorkspaceEffectsProps) {
  useEffect(() => {
    if (bootstrapPhase !== "ready") return;
    setAgentsVersion((value) => value + 1);
    void loadTaskTemplates();
    void loadKanbanTasks(currentRootIdRef.current);
    if (multiProjectSessionsEnabled) {
      void loadMultiProjectSessionGroups();
    }
  }, [
    bootstrapPhase,
    loadKanbanTasks,
    loadMultiProjectSessionGroups,
    loadTaskTemplates,
    multiProjectSessionsEnabled,
    setAgentsVersion,
  ]);
}
