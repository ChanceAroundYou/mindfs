import React from "react";
import { useI18n } from "../../i18n/index";
import { taskSessionGroupHeading } from "./sessionTaskGroups";

/**
 * 任务分组头（2026-11-20 用户决策：方案 A —— UI 层按 task_id 分组，零数据改动；
 * 同日改版：与普通会话行同名同字重，前置专属分组图标，文案为「任务名 # 任务号」，
 * 不再显示成员计数 —— 组内成员本身就是计数）。
 *
 * 刻意不用 button：它没有任何动作（既不能展开也不能折叠，成员永远可见），
 * 做成可聚焦元素只会把键盘用户引到一个按了没反应的东西上。真正的入口是下面
 * 每一张会话卡。
 */
export function TaskSessionGroupHeader({ taskId, label }: {
  taskId: string;
  label: string;
}) {
  const { t } = useI18n();
  const heading = taskSessionGroupHeading(label, taskId, t("sessionList.taskGroupFallback"));
  return (
    <div
      data-task-group={taskId}
      role="presentation"
      aria-label={t("sessionList.taskGroupTitle", { name: heading })}
      title={t("sessionList.taskGroupTitle", { name: heading })}
      // 外层几何对齐 SessionCard：2px 纵向留白 + 同样的内边距与 6px 间隙，
      // 名字因此落在与会话名完全相同的基线上。
      style={{
        width: "100%",
        display: "flex",
        alignItems: "center",
        padding: "2px 0",
        borderRadius: "8px",
        minWidth: 0,
      }}
    >
      <div
        style={{
          flex: 1,
          minWidth: 0,
          display: "flex",
          alignItems: "center",
          gap: "6px",
          padding: "7px 4px 7px 2px",
          border: "1px solid transparent",
        }}
      >
        <span
          style={{
            width: "18px",
            height: "18px",
            flexShrink: 0,
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "center",
            color: "var(--text-secondary)",
          }}
        >
          {/* 分组专属图标：三条逐层收短的横条 = 一组会话。与会话类型图标
              （气泡/终端/任务卡）刻意不同，扫一眼就知道这一行是容器不是会话。 */}
          <svg
            xmlns="http://www.w3.org/2000/svg"
            width="16"
            height="16"
            viewBox="0 0 24 24"
            aria-hidden="true"
            style={{ display: "block" }}
          >
            <path d="M0 0h24v24H0z" fill="none" />
            <g
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
            >
              <path d="M4 7h16" />
              <path d="M4 12h11" />
              <path d="M4 17h13" />
            </g>
          </svg>
        </span>
        <span
          style={{
            display: "block",
            minWidth: 0,
            fontSize: "13px",
            fontWeight: 500,
            lineHeight: 1.2,
            color: "var(--text-primary)",
            whiteSpace: "nowrap",
            overflow: "hidden",
            textOverflow: "ellipsis",
          }}
        >
          {heading}
        </span>
      </div>
    </div>
  );
}