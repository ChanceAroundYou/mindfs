/**
 * 跨项目工作台的共享样式。
 *
 * 色值纪律（commit 14df0d7 确立）：强调色**只**来自节点色，选中底色**只**是中性灰。
 * 所以这个文件里不得出现任何字面 hex —— 节点色是运行时从 nodeRegistry 注入的
 * （`hexToRgbaApp` 转成带透明度的同色边框/底色），其余一律走 index.css 的 token。
 * tests/workspace-board.test.mjs 会断言本文件不含 hex。
 */

import type React from "react";
import { rootBadgeButtonStyle } from "../../shared/rootBadgeStyle";

export const workspaceRootStyle: React.CSSProperties = {
  // 铺满：以前写死 maxHeight: calc(100dvh - 148px)，那 148px 猜的是 ActionBar
  // 高度 + 安全区，和真实值对不上，内容一少底部就空一大截。高度交给 flex 链。
  flex: 1,
  overflowY: "auto",
  // 刻意不带内边距：外层 DefaultListView 的 topContent 包裹层已经给了
  // （移动端 16/16/0，桌面 8/16/24）。这里原来还有一层 12px，两层叠加让工作台的
  // 边距和看板对不上——底边尤其宽出一截。去掉之后两个视图只由包裹层一处决定。
  display: "flex",
  flexDirection: "column",
  gap: "10px",
  minHeight: 0,
};

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
 * 轨道数跟着看板对齐：桌面一排 4 张，移动端一排 2 张。
 * 此前是 auto-fill + minmax(220px,1fr)，窄屏落到 1 列、宽屏能铺到 6、7 张，
 * 两头都不对；固定 repeat 之后列数只由 isMobile 决定，不受可用宽度摆布。
 */
export const workspaceTaskGridStyle = (isMobile = false): React.CSSProperties => ({
  display: "grid",
  // minmax(0,1fr) 的 0 下限是必要的：默认 minmax(auto,1fr) 会被卡片内容顶宽，
  // 多列在窄屏挤成一条。
  gridTemplateColumns: isMobile ? "repeat(2, minmax(0, 1fr))" : "repeat(4, minmax(0, 1fr))",
  gap: "6px",
});

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
