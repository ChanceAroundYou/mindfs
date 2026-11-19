import React from "react";
import { useI18n } from "../../i18n/index";
import { MAIN_SESSION_ICON_OFFSET } from "./sessionListShared";

/**
 * 任务分组头（2026-11-20 用户决策：方案 A —— UI 层按 task_id 分组，零数据改动，
 * 组内成员仍是原来的子会话样式）。
 *
 * 刻意不用 button：它没有任何动作（既不能展开也不能折叠，成员永远可见），
 * 做成可聚焦元素只会把键盘用户引到一个按了没反应的东西上。真正的入口是下面
 * 每一张会话卡。
 */
export function TaskSessionGroupHeader({ taskId, label, count }: {
  taskId: string;
  label: string;
  count: number;
}) {
  const { t } = useI18n();
  const name = label || t("sessionList.taskGroupFallback");
  const title = t("sessionList.taskGroupTitle", { name, count });
  return (
    <div
      data-task-group={taskId}
      role="presentation"
      aria-label={title}
      title={title}
      style={{
        display: "flex",
        alignItems: "center",
        gap: "5px",
        padding: `3px 6px 1px ${MAIN_SESSION_ICON_OFFSET}`,
        fontSize: "11px",
        lineHeight: 1.2,
        color: "var(--text-secondary)",
        minWidth: 0,
      }}
    >
      <svg
        xmlns="http://www.w3.org/2000/svg"
        width="1em"
        height="1em"
        viewBox="0 0 24 24"
        aria-hidden="true"
        style={{ width: "12px", height: "12px", flexShrink: 0 }}
      >
        <path d="M0 0h24v24H0z" fill="none" />
        <path
          fill="currentColor"
          d="M4 5a2 2 0 0 1 2-2h4l2 2h6a2 2 0 0 1 2 2v2H4zm0 5h16v8a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2z"
          opacity="0.85"
        />
      </svg>
      <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", minWidth: 0 }}>
        {name}
      </span>
      <span
        style={{
          flexShrink: 0,
          minWidth: "16px",
          padding: "0 5px",
          borderRadius: "999px",
          background: "rgba(148,163,184,0.18)",
          fontSize: "10px",
          lineHeight: "14px",
          textAlign: "center",
        }}
      >
        {count}
      </span>
    </div>
  );
}
