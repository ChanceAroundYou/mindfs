import React, { useState } from "react";
import { useI18n } from "../../i18n/index";
import type { TaskActivity } from "../../shared/taskComments";
import { ActivityMessage, type ActivityMessageProps } from "./ActivityMessage";

// 折叠后的运行汇报组：同一留言下、结算回复之后的 `purpose=progress` 行。
//
// 展开后复用 ActivityMessage 渲染原文 —— 汇报里有失败节点、未验证项等增量
// 信息，折叠只是默认收起，不是删掉。收起状态挂在组自己身上：每轮运行各一份，
// 用户展开一组不会连带展开别组。

export type ActivityReportsGroupProps = {
  parentId: string;
  reports: TaskActivity[];
} & Omit<ActivityMessageProps, "activity">;

export function ActivityReportsGroup({ parentId, reports, ...messageProps }: ActivityReportsGroupProps) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);

  return (
    <div
      data-testid="activity-reports-group"
      data-reports-parent={parentId}
      style={{ display: "flex", flexDirection: "column", gap: 8 }}
    >
      <button
        type="button"
        data-testid="activity-reports-toggle"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
        style={toggleStyle}
      >
        {open ? t("task.activity.reportsHide") : t("task.activity.reports", { count: reports.length })}
      </button>
      {open ? (
        <div data-testid="activity-reports" style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          {reports.map((report) => (
            <ActivityMessage key={report.id} activity={report} {...messageProps} />
          ))}
        </div>
      ) : null}
    </div>
  );
}

const toggleStyle: React.CSSProperties = {
  alignSelf: "flex-start",
  border: "1px solid var(--border-color)",
  background: "transparent",
  color: "var(--text-secondary)",
  borderRadius: 999,
  padding: "2px 10px",
  fontSize: 11,
  cursor: "pointer",
};
