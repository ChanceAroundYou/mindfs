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
 * 会话行的 aux 徽标（G-AD）：error/plan/todo/task。
 *
 * 2026-10-16 从「名字下方那一行」搬到会话名上方那排（SessionCardTopBadges），
 * 与 worktree 标签、子代理、问号排在一起 —— 会话行下面不再留任何东西。
 *
 * worktree 标签与子代理**不在这里**：它们由 SessionCardTopBadges 直接排在本组件之前。
 * ask_user 也不在这里 —— 它就是那个脉冲问号，再摆一个徽标等于同一信号响两遍。
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

  // 一个徽标都没有就不渲染，免得在上方那排留一段空 gap。
  if (badges.length === 0) {
    return null;
  }
  return (
    <span
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: "3px",
      }}
    >
      {badges.map((badge) => (
        <span
          key={badge.key}
          title={badge.label}
          aria-label={badge.key === "session-error" ? t("task.viewError") : badge.label}
          style={{ ...taskAuxBadgeStyle(badge.attention), pointerEvents: "auto" }}
        >
          <span style={taskAuxIconScaleStyle()}>{badge.icon}</span>
        </span>
      ))}
    </span>
  );
}

/**
 * 「有没有 aux 徽标」—— SessionCardTopBadges 的 hasSessionTopBadges 用它决定
 * 这排要不要整块渲染，SessionCard 再用同一个判据决定选中底色要不要向上拓展。
 * 判据只能有一处，否则会拓出一块盖着空气的深色底。
 */
export function hasSessionStatusBadges(auxFlags?: SessionAuxFlags): boolean {
  const flags = auxFlags || {};
  return Boolean(
    extractSessionErrorMessage(flags.session_error) ||
      flags.has_plan ||
      flags.has_todos ||
      flags.has_task,
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
