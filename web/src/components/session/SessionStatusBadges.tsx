import React from "react";
import { useI18n } from "../../i18n/index";
import { renderToolIcon } from "../stream/toolCallIcons";
import {
  TaskPlanAuxIcon,
  TaskSessionErrorIcon,
  taskAuxBadgeStyle,
  taskAuxIconScaleStyle,
} from "../task/taskIcons";
import type { SessionAuxFlags } from "../../shared/sessionTypes";

/**
 * 会话行的 aux 徽标条（G-AD）：error/plan/todo/task，摆在名字下方。
 *
 * worktree 标签**不在这里** —— 它绝对定位悬在会话名左上方，见 SessionWorktreeTag。
 * ask_user 也不在这里 —— 它就是图标与名字之间的脉冲问号（SessionCard 的 awaiting 位，
 * 由 aux_flags.ask_user_waiting 驱动），再摆一个徽标等于同一信号响两遍。
 *
 * 数据源是 toSessionItem 透传的 aux_flags。图标与样式全部复用任务卡那套
 * （taskIcons + stream/toolCallIcons，会话域早有 SessionViewer* 引用 stream 图标的
 * 先例），不自己画第二套。
 */
export function SessionStatusBadges({
  auxFlags,
  nodeColor,
}: {
  auxFlags?: SessionAuxFlags;
  nodeColor?: string;
}) {
  const { t } = useI18n();
  const flags = auxFlags || {};
  const sessionError = extractSessionErrorMessage(flags.session_error);
  const badges = [
    sessionError
      ? { key: "session-error", label: sessionError, icon: <TaskSessionErrorIcon />, attention: true }
      : null,
    flags.has_plan
      ? { key: "plan", label: t("task.hasPlan"), icon: <TaskPlanAuxIcon color={nodeColor || undefined} />, attention: false }
      : null,
    flags.has_todos
      ? { key: "todos", label: t("task.hasTodos"), icon: renderToolIcon("todo"), attention: false }
      : null,
    flags.has_task
      ? { key: "task", label: t("task.hasTask"), icon: renderToolIcon("task"), attention: false }
      : null,
  ].filter((item): item is { key: string; label: string; icon: React.ReactNode; attention: boolean } => Boolean(item));

  // 一个徽标都没有就整块不渲染：空的徽标条会带 marginTop 多撑一行，
  // 把只有名字的会话行顶高。
  if (badges.length === 0) {
    return null;
  }
  return (
    <span
      style={{
        marginTop: "2px",
        display: "flex",
        alignItems: "center",
        gap: "3px",
        flexWrap: "wrap",
      }}
    >
      {badges.map((badge) => (
        <span
          key={badge.key}
          title={badge.label}
          aria-label={badge.key === "session-error" ? t("task.viewError") : badge.label}
          style={taskAuxBadgeStyle(badge.attention)}
        >
          <span style={taskAuxIconScaleStyle()}>{badge.icon}</span>
        </span>
      ))}
    </span>
  );
}

// 错误原文可能是 JSON（{message, data}），tooltip 只取 message；
// 解析失败就原文照显。appTask.parseTaskSessionErrorMessage 的会话域本地版 ——
// view 层不 import app/（分层铁律，见 task-board-view-layering 测试同款约束）。
function extractSessionErrorMessage(error?: string): string {
  const raw = String(error || "").trim();
  if (!raw) return "";
  try {
    const parsed = JSON.parse(raw) as { message?: unknown };
    return typeof parsed.message === "string" && parsed.message.trim() ? parsed.message.trim() : raw;
  } catch {
    return raw;
  }
}
