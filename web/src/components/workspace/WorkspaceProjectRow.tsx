import React from "react";
import { useI18n } from "../../i18n";
import { useResponsive } from "../action/styleHelpers";
import { TaskGroupChevronIcon } from "../../app/taskIcons";
import type { SessionItem } from "../../app/appSession";
import type { WorkspaceProjectGroup, WorkspaceTaskItem } from "../../app/useWorkspaceBoard";
import type { TaskCardAction, TaskSessionErrorDialog } from "../task/TaskCardRows";
import { WorkspaceTaskRow } from "./WorkspaceTaskRow";
import {
  workspaceCountBadgeStyle,
  workspaceProjectGroupStyle,
  workspaceProjectHeaderStyle,
  workspaceProjectNameButtonStyle,
  workspaceProjectToggleStyle,
  workspaceTaskGridStyle,
} from "./workspaceStyles";

/**
 * 单个项目组：项目名 + 计数 + 折叠箭头，下面是任务卡网格。
 *
 * 头**只有两个热区**：项目名（跳该项目看板）和折叠箭头（展开/收起）。
 * 以前整行都能点，结果是想「扫一眼」的人也会被带跳走 —— 空白处、计数、节点色点
 * 都成了跳转陷阱。现在那几个不可点的部分老老实实当静态内容。
 *
 * 项目名前**没有色点**：名字本身已经是节点色（workspaceProjectNameButtonStyle），
 * 再配一个同色圆点等于把同一个信息说两遍，还占了名字的缩进。
 *
 * 折叠箭头刻意无框无底（见 workspaceProjectToggleStyle）：只看得见箭头本身，
 * 但热区仍是 40×24 —— 用留白而不是底纹来撑，原来的 12px 图标点不准。
 */
export function WorkspaceProjectRow({
  group,
  collapsed,
  onToggle,
  onOpenProject,
  onOpenTask,
  sessionKeysById,
  sessionByKey,
  getNodeColor,
  onMoveTask,
  onOpenSession,
  onShowSessionError,
}: {
  group: WorkspaceProjectGroup;
  collapsed: boolean;
  onToggle: (key: string) => void;
  onOpenProject: (rootId: string, nodeId: string) => void;
  onOpenTask: (item: WorkspaceTaskItem) => void;
  sessionKeysById: Record<string, string[]>;
  sessionByKey: Record<string, SessionItem>;
  getNodeColor: (rootId: string) => string | null;
  onMoveTask: (item: WorkspaceTaskItem, action: TaskCardAction) => void;
  onOpenSession: (sessionKey: string, rootId: string, taskId: string) => void;
  onShowSessionError: (payload: TaskSessionErrorDialog) => void;
}) {
  const { t } = useI18n();
  // 卡片列数跟着看板对齐：移动端一排 2 张、桌面 4 张。放在本组件里就地取，
  // 免得 WorkspaceBoard → 每行 → 网格一路透传 isMobile。
  const { isMobile } = useResponsive();
  const openProject = () => onOpenProject(group.rootId, group.nodeId);
  return (
    <section style={workspaceProjectGroupStyle}>
      <div style={workspaceProjectHeaderStyle}>
        <button
          type="button"
          title={group.rootName}
          onClick={openProject}
          style={workspaceProjectNameButtonStyle(group.color)}
        >
          {group.rootName}
        </button>
        {/* 「需要你 N」角标已移除（2026-10-07）：它和紧随其后的总数角标在「组内任务全部
            待审核」时显示同一个数字（用户实测「需要你3 3」）。待审核的信息没丢 ——
            每张卡标题行本来就有状态文字（· 待审核），「待审核」筛选档位也在。 */}
        <span style={workspaceCountBadgeStyle}>{group.tasks.length}</span>
        <button
          type="button"
          aria-label={collapsed ? t("task.workspaceExpand") : t("task.workspaceCollapse")}
          title={collapsed ? t("task.workspaceExpand") : t("task.workspaceCollapse")}
          onClick={() => onToggle(group.key)}
          style={workspaceProjectToggleStyle}
        >
          <TaskGroupChevronIcon collapsed={collapsed} />
        </button>
      </div>
      {collapsed ? null : (
        <div style={workspaceTaskGridStyle(isMobile)}>
          {group.tasks.map((item) => (
            <WorkspaceTaskRow
              key={`${item.nodeId}::${item.root_id}::${item.task.id}`}
              item={item}
              sessionKeysById={sessionKeysById}
              sessionByKey={sessionByKey}
              getNodeColor={getNodeColor}
              onOpen={onOpenTask}
              onMove={onMoveTask}
              onOpenSession={onOpenSession}
              onShowSessionError={onShowSessionError}
            />
          ))}
        </div>
      )}
    </section>
  );
}
