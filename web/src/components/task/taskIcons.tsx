/**
 * Task-domain icons and styles used by task cards and their workspace projection.
 *
 * Export inventory (kept in sync by task-icons-inventory.test.mjs):
 * Icons: ImportIcon, EditPencilIcon, HorizontalDotsIcon, PlusSmallIcon,
 * CheckIconSmall, TaskCompleteIcon, TaskQueuedSpinnerIcon, TaskPauseIcon,
 * TaskResumeIcon, TaskRebuildWorktreeIcon, TaskFinishWorktreeIcon,
 * TaskSessionErrorIcon, TaskCancelIcon, DeleteIcon, TaskPlanAuxIcon,
 * TaskExpandIcon, TaskGroupChevronIcon, RunNowIcon, SyncIcon,
 * ChevronDownSmallIcon.
 * Task styles: taskTemplateMenuItemStyle, taskCardSurfaceStyle,
 * taskCardIconButtonStyle, taskAuxBadgeStyle, taskAuxIconScaleStyle,
 * WorktreeTagState, taskWorktreeTagStyle, taskReplyPulseStyle.
 *
 * INVENTORY-END
 * The app/taskIcons.tsx entry is a compatibility facade for older consumers.
 */
import React from "react";
import { DEFAULT_NODE_COLOR } from "../../shared/nodePalette";
export * from "./taskStyles";

export function ImportIcon() {
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="currentColor"
      aria-hidden="true"
    >
      <path d="M19 9h-4V3H9v6H5l7 7 7-7zM5 18v2h14v-2H5z" />
    </svg>
  );
}

export function EditPencilIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path
        d="M4 20h4.2L18.7 9.5a2.1 2.1 0 0 0 0-3L17.5 5.3a2.1 2.1 0 0 0-3 0L4 15.8V20Z"
        stroke="currentColor"
        strokeWidth="1.8"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <path
        d="m13.5 6.3 4.2 4.2"
        stroke="currentColor"
        strokeWidth="1.8"
        strokeLinecap="round"
      />
    </svg>
  );
}

export function HorizontalDotsIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <circle cx="5" cy="12" r="1.8" />
      <circle cx="12" cy="12" r="1.8" />
      <circle cx="19" cy="12" r="1.8" />
    </svg>
  );
}

export function PlusSmallIcon() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" aria-hidden="true">
      <path d="M12 5v14" />
      <path d="M5 12h14" />
    </svg>
  );
}

export function CheckIconSmall() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path d="m5 12 4 4L19 6" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function TaskCompleteIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 16 16" aria-hidden="true">
      <path d="M0 0h16v16H0z" fill="none" />
      <path fill="currentColor" fillRule="evenodd" d="M3 13.5a.5.5 0 0 1-.5-.5V3a.5.5 0 0 1 .5-.5h9.25a.75.75 0 0 0 0-1.5H3a2 2 0 0 0-2 2v10a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V9.75a.75.75 0 0 0-1.5 0V13a.5.5 0 0 1-.5.5zm12.78-8.82a.75.75 0 0 0-1.06-1.06L9.162 9.177 7.289 7.241a.75.75 0 1 0-1.078 1.043l2.403 2.484a.75.75 0 0 0 1.07.01z" clipRule="evenodd" />
    </svg>
  );
}

export function TaskQueuedSpinnerIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.5"
      strokeLinecap="round"
      aria-hidden="true"
      style={{ animation: "mindfs-update-spin 0.9s linear infinite" }}
    >
      <path d="M21 12a9 9 0 1 1-6.2-8.56" />
    </svg>
  );
}

export function TaskPauseIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" style={{ display: "block" }}>
      <rect x="6" y="5" width="4" height="14" rx="1.2" />
      <rect x="14" y="5" width="4" height="14" rx="1.2" />
    </svg>
  );
}

export function TaskResumeIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="16"
      height="16"
      viewBox="0 0 24 24"
      aria-hidden="true"
      style={{ display: "block" }}
    >
      <path d="M0 0h24v24H0z" fill="none" />
      <path
        fill="currentColor"
        d="M14.5 4h.005M14.5 4L12 10l5 2.898L9.5 20l2.5-6l-5-2.9zm0-2a2.02 2.02 0 0 0-1.379.551L5.624 9.646a2 2 0 0 0-.61 1.686c.072.626.437 1.182.982 1.498l3.482 2.021l-1.826 4.381a2.003 2.003 0 0 0 1.847 2.77c.498 0 .993-.186 1.375-.548l7.5-7.103a2 2 0 0 0 .61-1.685a2 2 0 0 0-.982-1.498L14.52 9.15l1.789-4.293A2 2 0 0 0 14.5 2"
      />
    </svg>
  );
}

// 重建 worktree：环 + 顺时针箭头。跟已有的 worktree/git 图标区分开，
// 一眼能认出是「再来一次」而不是「去看它」。
export function TaskRebuildWorktreeIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      style={{ display: "block" }}
    >
      <path d="M3 12a9 9 0 0 1 15.3-6.4L21 8" />
      <path d="M21 3v5h-5" />
      <path d="M21 12a9 9 0 0 1-15.3 6.4L3 16" />
      <path d="M3 21v-5h5" />
    </svg>
  );
}

/**
 * worktree 收尾：把分支合回主干然后拆掉（git merge + worktree remove + branch -d）。
 *
 * 跟 TaskRebuildWorktreeIcon 刻意区分：那个是**重建**（目录没了要造回来，圆箭头），
 * 这个是**收尾**（活干完要落地，箭头朝内汇入主干）。两个动作长得像会让人以为是一回事，
 * 而一个修状态、一个改代码历史。
 */
export function TaskFinishWorktreeIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      style={{ display: "block" }}
    >
      {/* 源分支 */}
      <circle cx="6" cy="5" r="2.4" />
      <path d="M6 7.4V16" />
      <circle cx="6" cy="19" r="2.4" />
      {/* 汇入主干 */}
      <circle cx="18" cy="12" r="2.4" />
      <path d="M6 12h6" />
      <path d="M9.6 9.2 12 12l-2.4 2.8" />
    </svg>
  );
}

export function TaskSessionErrorIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v6" />
      <path d="M12 17h.01" />
    </svg>
  );
}

/**
 * 取消任务：圆圈加一道斜杠（作废符）。
 *
 * 与「删除」的垃圾桶刻意分开。取消只改状态 —— 卡片还在板上、还能翻回去看；
 * 删除才把卡片拿走。两个动作语义完全不同，以前共用一个垃圾桶图标，
 * 点之前根本分不清点下去会发生什么。
 */
export function TaskCancelIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.2"
      strokeLinecap="round"
      aria-hidden="true"
    >
      <circle cx="12" cy="12" r="9" />
      <path d="M5.6 5.6 18.4 18.4" />
    </svg>
  );
}

export function DeleteIcon() {
  return (
    <svg
      width="13"
      height="13"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <polyline points="3 6 5 6 21 6" />
      <path d="M19 6l-1 14H6L5 6" />
      <path d="M10 11v6" />
      <path d="M14 11v6" />
      <path d="M9 6V4h6v2" />
    </svg>
  );
}

export function TaskPlanAuxIcon({ color }: { color?: string } = {}) {
  const c = String(color || DEFAULT_NODE_COLOR).trim() || DEFAULT_NODE_COLOR;
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path d="M7 6h10M7 12h10M7 18h6" stroke={c} strokeWidth="2" strokeLinecap="round" />
      <path d="M4 6h.01M4 12h.01M4 18h.01" stroke={c} strokeWidth="3" strokeLinecap="round" />
    </svg>
  );
}

export function TaskExpandIcon({ collapsed }: { collapsed: boolean }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="13"
      height="13"
      viewBox="0 0 16 16"
      aria-hidden="true"
      style={{
        display: "block",
        transform: collapsed ? "none" : "rotate(180deg)",
      }}
    >
      <path d="M0 0h16v16H0z" fill="none" />
      <path fill="currentColor" d="M12.146 7.146a.5.5 0 0 1 .708.708l-4.5 4.5a.5.5 0 0 1-.708 0l-4.5-4.5a.5.5 0 1 1 .708-.708L8 11.293zm0-4a.5.5 0 0 1 .708.708l-4.5 4.5a.5.5 0 0 1-.708 0l-4.5-4.5a.5.5 0 1 1 .708-.708L8 7.293z" />
    </svg>
  );
}

export function TaskGroupChevronIcon({ collapsed }: { collapsed: boolean }) {
  return (
    <span
      aria-hidden="true"
      style={{
        flexShrink: 0,
        transform: collapsed ? "rotate(0deg)" : "rotate(90deg)",
        transition: "transform 0.2s",
        color: "currentColor",
        display: "inline-flex",
        alignItems: "center",
      }}
    >
      <svg
        width="12"
        height="12"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2.25"
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        <polyline points="9 18 15 12 9 6" />
      </svg>
    </span>
  );
}

export function RunNowIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="13"
      height="13"
      viewBox="0 0 24 24"
      aria-hidden="true"
    >
      <path d="M0 0h24v24H0z" fill="none" />
      <path
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        d="M20.409 9.353a2.998 2.998 0 0 1 0 5.294L7.597 21.614C5.534 22.737 3 21.277 3 18.968V5.033c0-2.31 2.534-3.769 4.597-2.648z"
      />
    </svg>
  );
}

export function SyncIcon({ style }: { style?: React.CSSProperties }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="13"
      height="13"
      viewBox="0 0 24 24"
      aria-hidden="true"
      style={style}
    >
      <path
        fill="currentColor"
        d="M19.91 15.51h-4.53a1 1 0 0 0 0 2h2.4A8 8 0 0 1 4 12a1 1 0 0 0-2 0a10 10 0 0 0 16.88 7.23V21a1 1 0 0 0 2 0v-4.5a1 1 0 0 0-.97-.99M12 2a10 10 0 0 0-6.88 2.77V3a1 1 0 0 0-2 0v4.5a1 1 0 0 0 1 1h4.5a1 1 0 0 0 0-2h-2.4A8 8 0 0 1 20 12a1 1 0 0 0 2 0A10 10 0 0 0 12 2"
      />
    </svg>
  );
}

export function ChevronDownSmallIcon() {
  return (
    <svg
      width="12"
      height="12"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="m6 9 6 6 6-6" />
    </svg>
  );
}
