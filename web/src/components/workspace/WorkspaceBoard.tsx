import React from "react";
import { useI18n } from "../../i18n";
import { useRefreshSpin } from "../../hooks";
import { useResponsive } from "../../app/appMisc";
import { SyncIcon } from "../../app/taskIcons";
import type { WorkspaceBoardFilter } from "../../app/appStorage";
import type { WorkspaceBoard, WorkspaceTaskItem } from "../../app/useWorkspaceBoard";
import { WorkspaceAttentionBar } from "./WorkspaceAttentionBar";
import { WorkspaceProjectRow } from "./WorkspaceProjectRow";
import { WorkspaceQuickLaunch } from "./WorkspaceQuickLaunch";
import {
  workspaceEmptyTextStyle,
  workspaceFilterButtonStyle,
  workspaceRootStyle,
  workspaceToolbarStyle,
} from "./workspaceStyles";

/**
 * 跨项目工作台容器。
 *
 * 信息架构（docs/workspace-design.md）：顶部「需要你」条带 + 按项目分组 + 底部快速发起。
 * 唯一按任务状态分区的地方是顶部条带，因为它回答的是「现在该我做什么」这个与项目无关的
 * 问题；其余按项目组织，回答「各项目在干什么」。跨项目维度只出现一次。
 *
 * 纯视图：数据由 useWorkspaceBoard 提供，回调全由 App 传入，组件内不持有业务状态。
 */
export type WorkspaceBoardProps = {
  board: WorkspaceBoard;
  filter: WorkspaceBoardFilter;
  onFilterChange: (filter: WorkspaceBoardFilter) => void;
  collapsedKeys: Set<string>;
  onToggleProject: (key: string) => void;
  onOpenProject: (rootId: string, nodeId: string) => void;
  onOpenTask: (item: WorkspaceTaskItem) => void;
  onMoveTask: (item: WorkspaceTaskItem, action: "complete" | "run-now" | "pause" | "resume") => void;
  onCreateTask: (rootId: string, nodeId: string, input: string) => void;
  onRefresh: () => void;
};

export function WorkspaceBoard({
  board, filter, onFilterChange, collapsedKeys, onToggleProject,
  onOpenProject, onOpenTask, onMoveTask, onCreateTask, onRefresh,
}: WorkspaceBoardProps) {
  const { t } = useI18n();
  const { isMobile } = useResponsive();
  const spin = useRefreshSpin(onRefresh);
  return (
    <div style={workspaceRootStyle}>
      <div style={workspaceToolbarStyle}>
        {([
          ["all", "task.workspaceFilterAll"],
          ["active", "task.workspaceFilterActive"],
          ["blocked", "task.workspaceFilterBlocked"],
        ] as Array<[WorkspaceBoardFilter, "task.workspaceFilterAll" | "task.workspaceFilterActive" | "task.workspaceFilterBlocked"]>).map(([value, key]) => (
          <button
            key={value}
            type="button"
            aria-pressed={filter === value}
            onClick={() => onFilterChange(value)}
            style={workspaceFilterButtonStyle(filter === value)}
          >
            {t(key)}
          </button>
        ))}
        <button
          type="button"
          title={t("common.refresh")}
          aria-label={t("common.refresh")}
          onClick={() => { void spin.handleClick(); }}
          onMouseDown={() => spin.setPressed(true)}
          onMouseUp={() => spin.setPressed(false)}
          onMouseLeave={() => spin.setPressed(false)}
          style={{
            marginLeft: "auto",
            height: "22px",
            width: "22px",
            borderRadius: "6px",
            border: "1px solid var(--border-color)",
            background: spin.pressed ? "var(--node-row-selected-bg)" : "transparent",
            color: "var(--text-secondary)",
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "center",
            cursor: "pointer",
          }}
        >
          <SyncIcon style={{ animation: spin.refreshing ? "mindfs-update-spin 0.8s linear infinite" : undefined }} />
        </button>
      </div>

      <WorkspaceAttentionBar items={board.blockedAll} onOpenTask={onOpenTask} isMobile={isMobile} />

      {board.loading && board.projects.length === 0 ? (
        <div style={workspaceEmptyTextStyle}>{t("task.loading")}</div>
      ) : board.projects.length === 0 ? (
        <div style={workspaceEmptyTextStyle}>{t("task.workspaceNoProjects")}</div>
      ) : (
        board.projects.map((group) => (
          <WorkspaceProjectRow
            key={group.key}
            group={group}
            collapsed={collapsedKeys.has(group.key)}
            onToggle={onToggleProject}
            onOpenProject={onOpenProject}
            onOpenTask={onOpenTask}
            onComplete={(item) => onMoveTask(item, "complete")}
            onRunNow={(item) => onMoveTask(item, "run-now")}
            onTogglePause={(item) => onMoveTask(item, item.task.status === "paused" ? "resume" : "pause")}
          />
        ))
      )}

      <WorkspaceQuickLaunch projects={board.projects} onCreateTask={onCreateTask} isMobile={isMobile} />
    </div>
  );
}
