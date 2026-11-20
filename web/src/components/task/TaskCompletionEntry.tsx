import React from "react";
import { useI18n } from "../../i18n/index";
import { TaskCompletionChecklist } from "./TaskCompletionChecklist";
import { useTaskCompletion } from "./useTaskCompletion";

// 完成证据入口：运行视图与监管记录下面的一条只读行，回答「这一轮还差什么证据」。
//
// 从来没有完成记录的任务整行隐藏（普通对话任务不该多出一个空清单）。有记录时
// 默认展开一小段：被拦下的项就是用户此刻最需要看到的东西，不该再点一次。
export type TaskCompletionEntryProps = {
  rootId: string;
  taskId: string;
  nodeId?: string;
  /** 任务头每次更新递增（父层传 task.updated_at）：复核结论可能在回合中途出现。 */
  refreshKey?: string;
};

export function TaskCompletionEntry({ rootId, taskId, nodeId, refreshKey }: TaskCompletionEntryProps) {
  const { t } = useI18n();
  const { completion, loaded, loading, error } = useTaskCompletion(rootId, taskId, nodeId, refreshKey);

  if (!loaded || !completion) return null;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "4px", marginTop: "8px" }} data-testid="task-completion-entry">
      {error ? (
        <div style={{ fontSize: "12px", color: "var(--danger-color, #ef4444)" }} role="alert">
          {error}
        </div>
      ) : null}
      <TaskCompletionChecklist completion={completion} />
      {loading ? (
        <div style={{ fontSize: "11px", color: "var(--text-secondary)" }}>{t("task.supervision.loading")}</div>
      ) : null}
    </div>
  );
}
