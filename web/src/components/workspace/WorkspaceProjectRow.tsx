import React from "react";
import { useI18n } from "../../i18n";
import { TaskGroupChevronIcon } from "../../app/taskIcons";
import type { WorkspaceProjectGroup, WorkspaceTaskItem } from "../../app/useWorkspaceBoard";
import { WorkspaceTaskRow } from "./WorkspaceTaskRow";
import {
  workspaceCountBadgeStyle,
  workspaceNodeDotStyle,
  workspaceProjectGroupStyle,
  workspaceProjectHeaderStyle,
  workspaceProjectHeaderHoverStyle,
  workspaceProjectNameStyle,
} from "./workspaceStyles";

/**
 * 单个项目组：可折叠头（节点色点 + 项目名 + 计数）+ 任务行列表。
 *
 * 头本身点开项目（R6：切到该项目并落到项目看板）；折叠箭头是独立热区，
 * 免得只想「扫一眼」的人总被误带跳走。折叠时只留头 —— 计数已经足够判断要不要展开。
 *
 * 组本身只在有匹配任务时才会被渲染（见 useWorkspaceBoard 的 filter），所以这里
 * 展开后必然非空，不需要「空项目」那一档。
 */
export function WorkspaceProjectRow({ group, collapsed, onToggle, onOpenProject, onOpenTask, onComplete, onRunNow, onTogglePause }: {
  group: WorkspaceProjectGroup;
  collapsed: boolean;
  onToggle: (key: string) => void;
  onOpenProject: (rootId: string, nodeId: string) => void;
  onOpenTask: (item: WorkspaceTaskItem) => void;
  onComplete: (item: WorkspaceTaskItem) => void;
  onRunNow: (item: WorkspaceTaskItem) => void;
  onTogglePause: (item: WorkspaceTaskItem) => void;
}) {
  const { t } = useI18n();
  return (
    <section style={workspaceProjectGroupStyle}>
      <div
        role="button"
        tabIndex={0}
        onClick={() => onOpenProject(group.rootId, group.nodeId)}
        onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); onOpenProject(group.rootId, group.nodeId); } }}
        style={{ ...workspaceProjectHeaderStyle, ...workspaceProjectHeaderHoverStyle }}
      >
        <span style={workspaceNodeDotStyle(group.color)} />
        <span style={workspaceProjectNameStyle}>{group.rootName}</span>
        {group.blockedCount > 0 ? <span style={workspaceCountBadgeStyle}>{t("task.workspaceAttention")} {group.blockedCount}</span> : null}
        <span style={workspaceCountBadgeStyle}>{group.tasks.length}</span>
        <span
          role="button"
          tabIndex={0}
          aria-label={collapsed ? t("task.workspaceExpand") : t("task.workspaceCollapse")}
          onClick={(event) => { event.stopPropagation(); onToggle(group.key); }}
          onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); event.stopPropagation(); onToggle(group.key); } }}
          style={{ marginLeft: "auto", display: "inline-flex", alignItems: "center", color: "var(--text-secondary)", cursor: "pointer" }}
        >
          <TaskGroupChevronIcon collapsed={collapsed} />
        </span>
      </div>
      {collapsed ? null : group.tasks.map((item) => (
        <WorkspaceTaskRow
          key={`${item.nodeId}::${item.root_id}::${item.task.id}`}
          item={item}
          onOpen={onOpenTask}
          onComplete={onComplete}
          onRunNow={onRunNow}
          onTogglePause={onTogglePause}
        />
      ))}
    </section>
  );
}
