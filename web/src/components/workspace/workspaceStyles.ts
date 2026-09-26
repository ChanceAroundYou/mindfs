/**
 * 跨项目工作台的共享样式。
 *
 * 色值纪律（commit 14df0d7 确立）：强调色**只**来自节点色，选中底色**只**是中性灰。
 * 所以这个文件里不得出现任何字面 hex —— 节点色是运行时从 nodeRegistry 注入的
 * （`hexToRgbaApp` 转成带透明度的同色边框/底色），其余一律走 index.css 的 token。
 * tests/workspace-board.test.mjs 会断言本文件不含 hex。
 */

import type React from "react";
import { hexToRgbaApp } from "../../app/taskIcons";
import { rootBadgeButtonStyle } from "../rootBadgeStyle";

/** 节点色 → 半透明底/边框；取不到节点色时回退中性 token（不猜一个颜色） */
export function nodeTint(color: string | null, alpha: number): string {
  const hex = String(color || "").trim();
  return /^#[0-9a-fA-F]{3,8}$/.test(hex) ? hexToRgbaApp(hex, alpha) : `var(--node-badge-bg)`;
}

export const workspaceRootStyle: React.CSSProperties = {
  overflowY: "auto",
  padding: "12px",
  display: "flex",
  flexDirection: "column",
  gap: "10px",
  minHeight: 0,
  maxHeight: "calc(100dvh - 148px)",
};

// 「需要你」条带：桌面横向滚，窄屏改纵向堆叠 ——
// 220px 的卡在 375px 主区里只放得下一张半，横向滚等于每张都要手动划一下。
export const workspaceAttentionBarStyle = (isMobile = false): React.CSSProperties => (isMobile
  ? { display: "flex", flexDirection: "column", gap: "6px" }
  : { display: "flex", gap: "6px", overflowX: "auto", paddingBottom: "2px" });

export const workspaceToolbarStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: "6px",
  flexWrap: "wrap",
};

/** 工具栏右侧动作区（新建任务 + 刷新）。整体贴右，内部两个键自然紧贴。 */
export const workspaceToolbarActionsStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: "6px",
  marginLeft: "auto",
};

export const workspaceCountBadgeStyle: React.CSSProperties = {
  height: "18px",
  borderRadius: "9px",
  background: "var(--node-badge-bg)",
  color: "var(--text-secondary)",
  padding: "0 7px",
  fontSize: "10px",
  fontWeight: 800,
  display: "inline-flex",
  alignItems: "center",
  whiteSpace: "nowrap",
};

export const workspaceEmptyTextStyle: React.CSSProperties = {
  fontSize: "12px",
  color: "var(--text-secondary)",
};

export const workspaceSectionStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "6px",
};

export const workspaceSectionHeaderStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: "6px",
};

export const workspaceSectionTitleStyle: React.CSSProperties = {
  fontSize: "12px",
  fontWeight: 800,
  color: "var(--text-secondary)",
};

export const workspaceAttentionCardStyle = (color: string | null, isMobile = false): React.CSSProperties => ({
  flex: "0 0 auto",
  width: isMobile ? "100%" : "220px",
  textAlign: "left",
  border: `1px solid ${nodeTint(color, 0.28)}`,
  borderRadius: "8px",
  background: "var(--menu-bg)",
  padding: "8px 10px",
  cursor: "pointer",
  display: "flex",
  flexDirection: "column",
  gap: "4px",
});

/**
 * 项目组：项目名 + 任务卡网格。整组是折叠的，所以没有外框，组与组之间靠间距。
 *
 * 头部只有两个热区：项目名（跳该项目看板）和折叠箭头。徽章、节点色点、计数都不可点 ——
 * 之前整个头都能点，反而让人以为点空白处也会跳。
 */
export const workspaceProjectGroupStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "6px",
};

export const workspaceProjectHeaderStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: "6px",
  minHeight: "26px",
};

/**
 * 项目名：直接复用左侧项目列表那套徽章样式（rootBadgeButtonStyle），
 * 只是把中性字色换成该项目的节点色。底色因此不再是「和任务行一样的灰」，
 * 而是一个有主色的名字 —— 层级一下就分开了。
 */
export const workspaceProjectNameButtonStyle = (color: string | null): React.CSSProperties => ({
  ...rootBadgeButtonStyle,
  color: String(color || "").trim() || "var(--text-primary)",
  cursor: "pointer",
  maxWidth: "55%",
  textAlign: "left",
});

/**
 * 折叠热区：只有 12px 的箭头太小了，整块撑成 40×24 的可点区。
 *
 * 刻意**不给**边框和底色（`background: transparent` + `border: none`）：
 * 项目头已经是「有底色的名字 + 中性徽章」，再加一个带框的按钮就三块底色打架，
 * 反而看不出该点哪。现在只留箭头，热区靠 minWidth/height 撑。
 */
export const workspaceProjectToggleStyle: React.CSSProperties = {
  marginLeft: "auto",
  minWidth: "40px",
  height: "24px",
  padding: "0 6px",
  borderRadius: "7px",
  border: "none",
  background: "transparent",
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  color: "var(--text-secondary)",
  cursor: "pointer",
  flexShrink: 0,
};

/**
 * 任务卡网格。卡面（边框/底/圆角/投影）和卡内两行都来自共享的
 * taskCardSurfaceStyle + TaskCardRows（看板那套卡片同一份），这里只管排布。
 *
 * 列宽对齐看板单列的 220px —— 150px 时长任务名几乎必被截断，而工作台是扫读场景，
 * 名字看不全等于这张卡白给。
 */
export const workspaceTaskGridStyle: React.CSSProperties = {
  display: "grid",
  gridTemplateColumns: "repeat(auto-fill, minmax(220px, 1fr))",
  gap: "6px",
};

/**
 * 「需要你」条带专用的两行小字：编号·项目名 / 阶段名。
 * 条带是横向滚的紧凑卡（220px 定宽），不是项目卡，所以这两条不归 TaskCardRows 管。
 */
export const workspaceTaskNumberStyle: React.CSSProperties = {
  fontSize: "11px",
  fontWeight: 800,
  color: "var(--text-secondary)",
  flexShrink: 0,
};

export const workspaceTaskMetaStyle: React.CSSProperties = {
  fontSize: "10px",
  color: "var(--text-secondary)",
  whiteSpace: "nowrap",
  flexShrink: 0,
};

/** 任务名：顶部「需要你」条带里那行字（项目卡的名字样式在 TaskCardRows 内） */
export const workspaceTaskNameStyle: React.CSSProperties = {
  fontSize: "12px",
  fontWeight: 600,
  color: "var(--text-color)",
  minWidth: 0,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

export const workspaceFilterButtonStyle = (active: boolean): React.CSSProperties => ({
  height: "22px",
  borderRadius: "6px",
  border: "1px solid var(--border-color)",
  background: active ? "var(--node-row-selected-bg)" : "transparent",
  color: active ? "var(--text-color)" : "var(--text-secondary)",
  padding: "0 9px",
  fontSize: "11px",
  fontWeight: 700,
  cursor: "pointer",
  whiteSpace: "nowrap",
});
