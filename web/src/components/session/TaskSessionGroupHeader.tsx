import React from "react";
import { useI18n } from "../../i18n/index";
import { taskSessionGroupHeading } from "./sessionTaskGroups";

/**
 * 任务分组头（2026-11-20 用户决策：方案 A —— UI 层按 task_id 分组，零数据改动）。
 *
 * 三次改版都记在这里，免得后来人把取舍当实现细节改回去：
 *   1. 与普通会话行同名同字重（13px / 500），前置分组专属图标；
 *   2. 文案「任务名 # 任务号」，不再显示成员计数；
 *   3. 默认折叠，点这一行展开/收起 —— 和子会话的展开同一个心智模型（用户
 *      2026-11-20：「默认不展开会话 除非点击进去」）；
 *   4. 不再单独画展开箭头，**图标自己就是开关**，且比会话行的圆略大一号（用户
 *      2026-11-20：「不要用下展开箭头 就用现在的icon 但是大小稍微变大一点
 *      它比普通对话的圆看起来小了一圈」）。
 *
 * 折叠状态由 useSessionChildTree 持有：默认收起，选中的会话在组内时自动展开，
 * 否则用户点进一条会话、刷新后它却藏起来，等于把选区弄丢。
 */
export function TaskSessionGroupHeader({
  taskId,
  label,
  number,
  expanded,
  onToggle,
}: {
  taskId: string;
  label: string;
  number: string;
  expanded: boolean;
  onToggle: () => void;
}) {
  const { t } = useI18n();
  const heading = taskSessionGroupHeading(label, number, t("sessionList.taskGroupFallback"));
  const [hovered, setHovered] = React.useState(false);
  return (
    <button
      type="button"
      data-task-group={taskId}
      aria-expanded={expanded}
      aria-label={t("sessionList.taskGroupTitle", { name: heading })}
      title={t("sessionList.taskGroupTitle", { name: heading })}
      onClick={onToggle}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      // 外层几何对齐 SessionCard：2px 纵向留白 + 同样的内边距与 6px 间隙，
      // 名字因此落在与会话名完全相同的基线上。
      style={{
        width: "100%",
        display: "flex",
        alignItems: "center",
        padding: "2px 0",
        border: "none",
        background: "transparent",
        borderRadius: "8px",
        minWidth: 0,
        cursor: "pointer",
        textAlign: "left" as const,
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
          borderRadius: "8px",
          border: "1px solid transparent",
          background: hovered ? "rgba(148,163,184,0.12)" : "transparent",
          transition: "background 0.15s ease",
        }}
      >
        <span
          style={{
            // 20px 图标位 / 18px 图形：会话行是 18px 位 / 16px 图形（外加 10px 的
            // agent 角标），分组图标略大一号才不会看着「比普通对话的圆小一圈」。
            width: "20px",
            height: "20px",
            flexShrink: 0,
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "center",
            // 分组图标跟随主题色（用户 2026-11-20）：它是容器不是会话，
            // 但也不该灰到看不见 —— 主题色一眼能把「一个任务」从会话堆里分出来。
            color: "var(--accent-color)",
            // 没有箭头了，图标自己是可点的开关：悬停时提亮并轻微放大，
            // 让「这一行能点开」有直接反馈。
            opacity: hovered ? 1 : 0.9,
            transform: hovered ? "scale(1.08)" : "none",
            transition: "opacity 0.15s ease, transform 0.15s ease",
          }}
        >
          {/* 语义图标：两层堆叠的卡片 = 同一任务下叠着多条会话。与会话类型图标
              （气泡/终端/任务卡）刻意不同，扫一眼就知道这一行是容器不是会话。 */}
          <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" aria-hidden="true" style={{ display: "block" }}>
            <path d="M0 0h24v24H0z" fill="none" />
            <g fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
              <rect x="4" y="4" width="13" height="9" rx="2" />
              <path d="M8 17h8a4 4 0 0 0 4-4V9" />
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
    </button>
  );
}
