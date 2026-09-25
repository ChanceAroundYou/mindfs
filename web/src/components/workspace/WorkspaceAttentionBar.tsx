import React from "react";
import { useI18n } from "../../i18n";
import type { WorkspaceTaskItem } from "../../app/useWorkspaceBoard";
import {
  workspaceAttentionBarStyle,
  workspaceAttentionCardStyle,
  workspaceCountBadgeStyle,
  workspaceSectionHeaderStyle,
  workspaceSectionStyle,
  workspaceSectionTitleStyle,
  workspaceTaskMetaStyle,
  workspaceTaskNameStyle,
  workspaceTaskNumberStyle,
} from "./workspaceStyles";

/**
 * 顶部「需要你」条带：整个工作台里唯一按任务状态组织的区域。
 *
 * 为什么它能例外：它回答的是一个与项目无关的问题 ——「现在该我做什么」。
 * 其余全部按项目组织，回答「各项目在干什么」。跨项目维度只在这里出现一次，
 * 不会出现第二处需要按状态分区的跨项目视图。
 */
export function WorkspaceAttentionBar({ items, onOpenTask, isMobile, getNodeColor }: {
  items: WorkspaceTaskItem[];
  onOpenTask: (item: WorkspaceTaskItem) => void;
  isMobile: boolean;
  /** 按任务所属项目取节点色；条带是跨项目的，卡片得自己认自己的项目 */
  getNodeColor: (rootId: string) => string | null;
}) {
  const { t } = useI18n();
  // 没人等时整条收起，不占空间（不渲染空壳标题）
  if (items.length === 0) return null;
  return (
    <section style={workspaceSectionStyle}>
      <header style={workspaceSectionHeaderStyle}>
        <span style={workspaceSectionTitleStyle}>{t("task.workspaceAttention")}</span>
        <span style={workspaceCountBadgeStyle}>{items.length}</span>
      </header>
      <div style={workspaceAttentionBarStyle(isMobile)}>
        {items.map((item) => (
          <button
            key={`${item.nodeId}::${item.root_id}::${item.task.id}`}
            type="button"
            onClick={() => onOpenTask(item)}
            style={workspaceAttentionCardStyle(getNodeColor(item.root_id), isMobile)}
          >
            <span style={workspaceTaskNumberStyle}>
              {item.task.task_number ? `#${item.task.task_number}` : ""} · {item.root_name}
            </span>
            <span style={workspaceTaskNameStyle}>
              {item.task.name || item.root_name || item.task.task_template_name || t("task.unnamedTemplate")}
            </span>
            {item.task.current_stage_name ? (
              <span style={workspaceTaskMetaStyle}>{item.task.current_stage_name}</span>
            ) : null}
          </button>
        ))}
      </div>
    </section>
  );
}
