import React from "react";
import { useI18n } from "../../i18n/index";
import type { TaskCompletionView } from "../../shared/taskCompletion";
import { SupervisionEntryList } from "./SupervisionEntryList";
import { useSupervisionEntry } from "./useSupervisionEntry";

// 监管记录面板：与执行详情并排的另一个胶囊展开出来的内容，回答「Router 有没有
// 替我自己动过手」。记录是只读证据——动作的结果已经落在任务里（重试的节点、
// 交给用户的说明），这里不提供操作。
//
// 两个胶囊始终存在且互斥展开（见 TaskInspectEntries），所以没有记录时这里给
// 一句空态说明，而不是让按钮忽隐忽现。

export type SupervisionEntryProps = {
  rootId: string;
  taskId: string;
  nodeId?: string;
  /** 任务头每次更新递增（父层传 task.updated_at）：新的监管决策可能在回合中途出现。 */
  refreshKey?: string;
  /** 完成证据快照（父层已拉过）；有值时时间线里会出现「完成判定」行。 */
  completion?: TaskCompletionView | null;
  /** 由同一行的胶囊组控制；false 时面板不渲染内容（hook 仍挂载以保留状态）。 */
  open: boolean;
};

export function SupervisionEntry({
  rootId,
  taskId,
  nodeId,
  refreshKey,
  completion,
  open,
}: SupervisionEntryProps) {
  const { t } = useI18n();
  const { rows, loaded, loading, error, supervisionEnabled } = useSupervisionEntry(
    rootId,
    taskId,
    nodeId,
    refreshKey,
    completion,
  );

  if (!open) return null;

  const emptyKey =
    supervisionEnabled === false
      ? "task.supervision.empty.disabled"
      : "task.supervision.empty.idle";

  return (
    <div id="task-supervision-panel" data-testid="supervision-entry" style={panelStyle}>
      {error ? (
        <div style={{ fontSize: "12px", color: "var(--danger-color, #ef4444)" }} role="alert">
          {error}
        </div>
      ) : null}
      {loaded && rows.length === 0 ? <div style={hintStyle}>{t(emptyKey)}</div> : null}
      {rows.length > 0 ? <SupervisionEntryList rows={rows} /> : null}
      {!loaded || loading ? <div style={hintStyle}>{t("task.supervision.loading")}</div> : null}
    </div>
  );
}

const panelStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "6px",
};

const hintStyle: React.CSSProperties = {
  fontSize: "11px",
  color: "var(--text-secondary)",
};
