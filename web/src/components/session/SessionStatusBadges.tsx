import React from "react";
import { useI18n } from "../../i18n/index";
import { renderToolIcon } from "../stream/toolCallIcons";
import { NoWorktreeIcon } from "../common/NoWorktreeIcon";
import {
  TaskPlanAuxIcon,
  TaskSessionErrorIcon,
  taskAuxBadgeStyle,
  taskWorktreeTagStyle,
} from "../task/taskIcons";
import type { RelatedWorktree, SessionAuxFlags } from "../../shared/sessionTypes";

/**
 * 会话行的内容徽标条（G-AD）：worktree 标签 + error/plan/todo/task 徽标。
 *
 * 数据源是 toSessionItem 透传的 aux_flags / related_worktree / worktree_missing，
 * 零徽标时只剩 worktree 标签（有没有 worktree 本身就是信息，见 TaskCardRows 同款）。
 *
 * ask_user 不在这里 —— 它就是图标与名字之间的脉冲问号（SessionCard 的 awaiting 位，
 * 由 aux_flags.ask_user_waiting 驱动），再摆一个徽标等于同一信号响两遍。
 * 图标与样式全部复用任务卡那套（taskIcons + stream/toolCallIcons，会话域早有
 * SessionViewer* 引用 stream 图标的先例），不自己画第二套。
 */
export function SessionStatusBadges({
  auxFlags,
  relatedWorktree,
  worktreeMissing,
  nodeColor,
}: {
  auxFlags?: SessionAuxFlags;
  relatedWorktree?: RelatedWorktree | null;
  worktreeMissing?: boolean;
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

  const worktreePath = String(relatedWorktree?.path || "").trim();
  // 会话没有任务的 worktree_built 位：path 在且目录没被删 = 有树；
  // path 还在但目录已删 = 已收尾；本来就没 path = 没开过。
  const worktreeState = !worktreePath || worktreeMissing ? (worktreePath ? "finished" : "none") : "enabled";
  const worktreeTitle =
    worktreeState === "finished"
      ? t("task.worktreeFinishedTitle")
      : worktreeState === "enabled"
        ? t("task.worktreeTitle")
        : t("task.noWorktreeTitle");

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
      <span
        title={worktreeTitle}
        aria-label={worktreeTitle}
        style={{ ...taskWorktreeTagStyle(worktreeState), marginLeft: 0 }}
      >
        {worktreeState === "enabled" ? null : <NoWorktreeIcon />}
        {worktreeState === "finished" ? t("task.worktreeFinishedLabel") : "worktree"}
      </span>
      {badges.map((badge) => (
        <span
          key={badge.key}
          title={badge.label}
          aria-label={badge.key === "session-error" ? t("task.viewError") : badge.label}
          style={taskAuxBadgeStyle(badge.attention)}
        >
          {badge.icon}
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
