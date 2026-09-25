import React from "react";
import { useI18n } from "../../i18n";
import { TaskGroupChevronIcon } from "../../app/taskIcons";
import type { WorkspaceProjectGroup, WorkspaceTaskItem } from "../../app/useWorkspaceBoard";
import { WorkspaceTaskRow } from "./WorkspaceTaskRow";
import {
  workspaceCountBadgeStyle,
  workspaceEmptyTextStyle,
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
  // 终态排在活跃之后：默认先看还在动的东西
  const shown = collapsed ? [] : [...group.active, ...group.ended];
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
        {group.blocked.length > 0 ? <span style={workspaceCountBadgeStyle}>{t("task.workspaceAttention")} {group.blocked.length}</span> : null}
        {group.active.length > 0 ? <span style={workspaceCountBadgeStyle}>{group.active.length}</span> : null}
        {group.sessionCount > 0 ? <span style={workspaceCountBadgeStyle}>{group.sessionCount} {t("task.workspaceSessions")}</span> : null}
        {group.ended.length > 0 ? <span style={workspaceCountBadgeStyle}>{group.ended.length} {t("task.workspaceEnded")}</span> : null}
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
      {collapsed ? null : shown.length === 0 ? (
        // 空项目也必须占一行 —— 看不见的项目等于忘了它。建任务走底部快速发起（那里能选项目），
        // 不在这里塞第二个入口：两个入口指向同一件事，多一个就多一处会失同步的界面。
        <div style={workspaceEmptyTextStyle}>{t("task.workspaceEmptyProject")}</div>
      ) : (
        shown.map((item) => (
          <WorkspaceTaskRow
            key={`${item.nodeId}::${item.root_id}::${item.task.id}`}
            item={item}
            onOpen={onOpenTask}
            onComplete={onComplete}
            onRunNow={onRunNow}
            onTogglePause={onTogglePause}
          />
        ))
      )}
    </section>
  );
}
