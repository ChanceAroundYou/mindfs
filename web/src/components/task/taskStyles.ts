import type React from "react";
import { DEFAULT_NODE_COLOR } from "../../shared/nodePalette";
import { hexToRgba } from "../../shared/hexToRgba";



export function taskTemplateMenuItemStyle(disabled = false): React.CSSProperties {
  return {
    width: "100%",
    minHeight: "30px",
    border: "none",
    borderRadius: "8px",
    background: "transparent",
    color: "var(--text-primary)",
    display: "flex",
    alignItems: "center",
    gap: "8px",
    padding: "6px 8px",
    textAlign: "left",
    fontSize: "12px",
    cursor: disabled ? "not-allowed" : "pointer",
    opacity: disabled ? 0.45 : 1,
    boxSizing: "border-box",
  };
}



/**
 * 任务卡卡面（边框 + 底 + 投影 + 圆角）。
 *
 * 抽出来是因为两处要用同一张卡：项目看板的卡片、以及跨项目工作台上的小卡。
 * 以前工作台自己手搓了一份几乎一样的灰底，两边各长各的。
 */
export function taskCardSurfaceStyle(selected = false): React.CSSProperties {
  return {
    position: "relative",
    border: selected ? "1px solid rgba(14, 165, 233, 0.95)" : "1px solid rgba(96, 165, 250, 0.42)",
    borderRadius: "8px",
    background: "var(--menu-bg)",
    boxShadow: selected ? "0 0 0 2px rgba(14, 165, 233, 0.16)" : "0 1px 2px rgba(15, 23, 42, 0.06)",
    cursor: "pointer",
  };
}



export function taskCardIconButtonStyle(tone: "default" | "accent" | "success" | "danger" | "warning" = "default"): React.CSSProperties {
  return {
    width: "22px",
    height: "22px",
    border: "none",
    borderRadius: "6px",
    background: "transparent",
    color: tone === "accent" ? "var(--accent-color)" : tone === "success" ? "#16a34a" : tone === "danger" ? "#dc2626" : tone === "warning" ? "#d97706" : "var(--text-secondary)",
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    cursor: "pointer",
    padding: 0,
  };
}



export function taskAuxBadgeStyle(attention = false): React.CSSProperties {
  return {
    width: "18px",
    height: "18px",
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    borderRadius: "5px",
    background: attention ? "rgba(239, 68, 68, 0.10)" : "transparent",
    color: "var(--text-secondary)",
    animation: attention ? "mindfs-task-ask-user-pulse 2.2s ease-in-out infinite" : "none",
  };
}



// 四档：enabled（有活着的 worktree）/ finishing（收尾流程正在跑）/
// finished（建过、目录已经不在 = 已收尾）/ none（本来就没开，或从来没建成）。
//
// 曾经有第五档 missing（开过、目录被删了，红色警报）。2026-10-05 删掉：目录消失
// 是「已收尾」的金标准 —— 没有目录就既没有树可执行、也没有树可拆，红色警报承诺的
// 「点重建恢复后再执行」在收完尾的任务上根本无从兑现，实测 14 个历史任务全挂这个。
// finishing 单列一档而不是拿 running 去顶：收尾会**拆掉**这个目录，
// 期间 worktree 确实还在、也还挂着分支，混进 enabled 会让用户以为「还没开始」，
// 于是再去点一次收尾。
export type WorktreeTagState = "enabled" | "finishing" | "finished" | "none";



export function taskWorktreeTagStyle(state: WorktreeTagState): React.CSSProperties {
  const finished = state === "finished";
  const enabled = state === "enabled";
  const finishing = state === "finishing";
  const tone = finished
    ? "rgba(100, 116, 139, "
    : enabled || finishing
      ? "rgba(22, 163, 74, "
      : "rgba(217, 119, 6, ";
  return {
    flex: "0 0 auto",
    marginLeft: "auto",
    display: "inline-flex",
    alignItems: "center",
    gap: "1px",
    border: `1px solid ${tone}0.28)`,
    borderRadius: "4px",
    background: `${tone}0.08)`,
    color: finished ? "#475569" : enabled || finishing ? "#15803d" : "#b45309",
    fontSize: "9px",
    fontWeight: 800,
    lineHeight: "12px",
    padding: enabled ? "0 4px" : "0 3px 0 2px",
    // 收尾中不加脉冲动画（2026-10-07 用户要求）：区分 enabled 与 finishing 的信号
    // 就是标签文字本身（worktree / 收尾中）。
  };
}



// 无效 hex 的兜底色 = local 节点色。预先展开成 rgb 分量，避免递归解析。
export function taskReplyPulseStyle(color?: string | null): React.CSSProperties {
  const c = String(color || DEFAULT_NODE_COLOR).trim() || DEFAULT_NODE_COLOR;
  return {
    position: "absolute",
    top: "6px",
    right: "6px",
    width: "8px",
    height: "8px",
    borderRadius: "999px",
    boxSizing: "border-box",
    border: `1.5px solid ${c}`,
    background: c,
    animation: "mindfs-bound-pulse 2.2s ease-in-out infinite",
    boxShadow: `0 0 0 1.5px ${hexToRgba(c, 0.14)}`,
    pointerEvents: "none",
  };
}
