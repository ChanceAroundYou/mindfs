import React from "react";
import { useI18n } from "../../i18n/index";
import { SupervisionEntryList } from "./SupervisionEntryList";
import { useSupervisionEntry } from "./useSupervisionEntry";

// 监管记录入口：运行视图下面的一条可展开行，回答「Router 有没有替我自己动过手」。
//
// 没有任何监管决策时整行隐藏（普通任务不该多出一个永远空的按钮）。记录是只读
// 证据——动作的结果已经落在任务里（重试的节点、交给用户的说明），这里不提供操作。

export type SupervisionEntryProps = {
  rootId: string;
  taskId: string;
  nodeId?: string;
  /** 任务头每次更新递增（父层传 task.updated_at）：新的监管决策可能在回合中途出现。 */
  refreshKey?: string;
};

export function SupervisionEntry({ rootId, taskId, nodeId, refreshKey }: SupervisionEntryProps) {
  const { t } = useI18n();
  const { rows, loaded, open, loading, error, toggle } = useSupervisionEntry(rootId, taskId, nodeId, refreshKey);

  if (!loaded || rows.length === 0) return null;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "6px", marginTop: "8px" }} data-testid="supervision-entry">
      <div style={{ display: "flex", alignItems: "center", gap: "6px" }}>
        <button
          type="button"
          onClick={toggle}
          aria-expanded={open}
          data-testid="supervision-toggle"
          style={{
            alignSelf: "flex-start",
            border: "1px solid var(--border-color)",
            background: "transparent",
            color: "var(--text-secondary)",
            borderRadius: "6px",
            padding: "3px 8px",
            fontSize: "12px",
            cursor: "pointer",
          }}
        >
          {open ? t("task.supervision.collapse") : t("task.supervision.expand")}
        </button>
      </div>
      {open ? (
        <>
          {error ? (
            <div style={{ fontSize: "12px", color: "var(--danger-color, #ef4444)" }} role="alert">
              {error}
            </div>
          ) : null}
          <SupervisionEntryList rows={rows} />
          {loading ? (
            <div style={{ fontSize: "11px", color: "var(--text-secondary)" }}>{t("task.supervision.loading")}</div>
          ) : null}
        </>
      ) : null}
    </div>
  );
}
