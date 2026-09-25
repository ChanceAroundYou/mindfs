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

export const workspaceToolbarStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: "6px",
  flexWrap: "wrap",
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

/** 「需要你」条带：横向滚动的紧凑卡，没东西时整条不渲染 */
export const workspaceAttentionBarStyle: React.CSSProperties = {
  display: "flex",
  gap: "6px",
  overflowX: "auto",
  paddingBottom: "2px",
};

export const workspaceAttentionCardStyle = (color: string | null): React.CSSProperties => ({
  flex: "0 0 auto",
  width: "220px",
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

/** 项目组：可点开的头 + 任务行列表。整组是折叠的，所以没有外框，组与组之间靠间距 */
export const workspaceProjectGroupStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "4px",
};

export const workspaceProjectHeaderStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: "8px",
  minHeight: "26px",
  padding: "2px 4px",
  borderRadius: "7px",
  cursor: "pointer",
};

export const workspaceProjectHeaderHoverStyle: React.CSSProperties = {
  background: "var(--node-row-selected-bg)",
};

/** 节点色点：工作台上唯一允许的彩色，且唯一来源是节点色本身 */
export const workspaceNodeDotStyle = (color: string | null): React.CSSProperties => ({
  width: "8px",
  height: "8px",
  borderRadius: "50%",
  background: color || "var(--text-secondary)",
  flexShrink: 0,
});

export const workspaceProjectNameStyle: React.CSSProperties = {
  fontSize: "13px",
  fontWeight: 800,
  color: "var(--text-color)",
  minWidth: 0,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

/** 任务行：紧凑单行，不是看板那种大卡 —— 工作台只回答「各项目在干什么」 */
export const workspaceTaskRowStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: "8px",
  minHeight: "28px",
  padding: "3px 8px",
  borderRadius: "7px",
  background: "var(--menu-bg)",
  cursor: "pointer",
};

export const workspaceTaskRowHoverStyle: React.CSSProperties = {
  background: "var(--node-row-selected-bg)",
};

export const workspaceTaskNumberStyle: React.CSSProperties = {
  fontSize: "11px",
  fontWeight: 800,
  color: "var(--text-secondary)",
  flexShrink: 0,
};

export const workspaceTaskNameStyle: React.CSSProperties = {
  fontSize: "12px",
  fontWeight: 600,
  color: "var(--text-color)",
  minWidth: 0,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
};

export const workspaceTaskMetaStyle: React.CSSProperties = {
  fontSize: "10px",
  color: "var(--text-secondary)",
  whiteSpace: "nowrap",
  flexShrink: 0,
};

export const workspaceEndedTextStyle: React.CSSProperties = {
  fontSize: "11px",
  color: "var(--text-secondary)",
  padding: "2px 8px",
};

/** 快速发起常驻底部：空项目也在这里被看见并被就地建任务 */
export const workspaceQuickLaunchStyle: React.CSSProperties = {
  border: "1px dashed var(--border-color)",
  borderRadius: "10px",
  padding: "8px",
  display: "flex",
  alignItems: "center",
  gap: "8px",
  flexWrap: "wrap",
  background: "var(--menu-bg)",
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
