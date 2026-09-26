import React from "react";
import { useI18n } from "../../i18n";
import { useRefreshSpin } from "../../hooks";
import { useResponsive } from "../../app/appMisc";
import { SyncIcon } from "../../app/taskIcons";
import type { WorkspaceBoardFilter } from "../../app/appStorage";
import type { WorkspaceBoard, WorkspaceTaskItem } from "../../app/useWorkspaceBoard";
import type { TaskCardAction, TaskSessionErrorDialog } from "../TaskCardRows";
import type { SessionItem } from "../../app/appSession";
import { WorkspaceAttentionBar } from "./WorkspaceAttentionBar";
import { WorkspaceProjectRow } from "./WorkspaceProjectRow";
import { WorkspaceQuickLaunch } from "./WorkspaceQuickLaunch";
import type { TaskTemplate } from "../../services/tasks";
import {
  workspaceEmptyTextStyle,
  workspaceFilterButtonStyle,
  workspaceRootStyle,
  workspaceToolbarActionsStyle,
  workspaceToolbarStyle,
} from "./workspaceStyles";

/**
 * 跨项目工作台容器。
 *
 * 信息架构（docs/workspace-design.md）：顶部工具栏（筛选 + 快速发起 + 刷新）
 * + 「需要你」条带 + 按项目分组。唯一按任务状态分区的地方是顶部条带，因为它回答的
 * 是「现在该我做什么」这个与项目无关的问题；其余按项目组织，回答「各项目在干什么」。
 * 跨项目维度只出现一次。
 *
 * 没有任务的项目不出现（筛选后为空壳的也不出现）—— 面板上的每一行都必须是件真事。
 *
 * 纯视图：数据由 useWorkspaceBoard 提供，回调全由 App 传入，组件内不持有业务状态。
 */
export type WorkspaceBoardProps = {
  board: WorkspaceBoard;
  filter: WorkspaceBoardFilter;
  onFilterChange: (filter: WorkspaceBoardFilter) => void;
  collapsedKeys: Set<string>;
  onToggleProject: (key: string) => void;
  getNodeColor: (rootId: string) => string | null;
  onOpenProject: (rootId: string, nodeId: string) => void;
  onOpenTask: (item: WorkspaceTaskItem) => void;
  onMoveTask: (item: WorkspaceTaskItem, action: TaskCardAction) => void;
  /** 卡片里的会话 chip 要跳会话，跨项目所以必须带 root 过去 */
  onOpenSession: (sessionKey: string, rootId: string, taskId: string) => void;
  /** 卡片上的会话错误入口要弹同一个错误面板（App 层那个） */
  onShowSessionError: (payload: TaskSessionErrorDialog) => void;
  taskSessionKeysById: Record<string, string[]>;
  sessionByKey: Record<string, SessionItem>;
  /** 新建任务：直接打开新建任务面板（面板自带「项目」下拉），不自己挑完再转一手 */
  templates: TaskTemplate[];
  onCreateTask: (rootId: string, nodeId: string, template: TaskTemplate) => void;
  onRefresh: () => void;
};

export function WorkspaceBoard({
  board, filter, onFilterChange, collapsedKeys, onToggleProject, getNodeColor,
  onOpenProject, onOpenTask, onMoveTask, onOpenSession, onShowSessionError,
  taskSessionKeysById, sessionByKey, templates, onCreateTask, onRefresh,
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
        {/* 右侧动作区：「新建」和「重新拉一遍」都属于对整个面板的动作，挨着放右上角。
            marginLeft:auto 挂在这个**容器**上（以前挂在刷新键自身），容器只有两个子元素，
            于是它整体贴右、内部自然紧贴 —— 筛选组则老老实实留在左边。
            新建放刷新左边：刷新是重复动作，新建是创建动作，后者读起来在前。 */}
        <div style={workspaceToolbarActionsStyle}>
          <WorkspaceQuickLaunch projects={board.projects} templates={templates} onPick={onCreateTask} />
          <button
            type="button"
            title={t("common.refresh")}
            aria-label={t("common.refresh")}
            onClick={() => { void spin.handleClick(); }}
            onMouseDown={() => spin.setPressed(true)}
            onMouseUp={() => spin.setPressed(false)}
            onMouseLeave={() => spin.setPressed(false)}
            style={{
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
      </div>

      <WorkspaceAttentionBar items={board.blockedAll} onOpenTask={onOpenTask} isMobile={isMobile} getNodeColor={getNodeColor} />

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
            sessionKeysById={taskSessionKeysById}
            sessionByKey={sessionByKey}
            getNodeColor={getNodeColor}
            onMoveTask={onMoveTask}
            onOpenSession={onOpenSession}
            onShowSessionError={onShowSessionError}
          />
        ))
      )}
    </div>
  );
}
