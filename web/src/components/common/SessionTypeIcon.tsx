import React from "react";
import { AgentIcon } from "../agent/AgentIcon";
import { ModeIcon, type ModeIconType } from "../agent/ModeIcon";

/**
 * 「这是一条什么会话」的图标位：会话类型图标（ModeIcon）+ 右下角角标。
 *
 * 2026-11-19 从 `session/SessionCardIcon` 抽出（原实现只服务会话列表）。抽出的理由是
 * 同一套视觉出现了第二次：任务详情的运行视图要按用户要求使用「右侧会话列表里的那种」
 * 跳转图标，两处各写一份样式迟早会漂。
 *
 * 几何约束（会话列表原本的注释保持不变）：18px 定位盒是角标的基准，两个角标都
 * `position: absolute` 挂在右下角；盒子尺寸一改角标就跟着漂。
 *
 * 角标有两种，由调用方决定：
 * - `shellLabel` 给命令会话用（终端名缩写，右侧外挂的蓝色胶囊）；
 * - 否则是 agent 角标（圆底 + AgentIcon），空 agent 也渲染空圆底，与会话列表一致。
 */
export type SessionTypeIconProps = {
  /** 会话类型；任务会话（workflow worker）由调用方传 `task`。 */
  type: ModeIconType;
  /** agent 名，决定右下角角标图标。 */
  agent?: string;
  /** 命令会话的终端缩写；给了它就用胶囊角标替代 agent 角标。 */
  shellLabel?: string;
  /** 胶囊的悬停标题（原始终端名）。 */
  shellTitle?: string;
  /** 类型图标的着色（跨节点会话用节点色）。 */
  color?: string;
};

export function SessionTypeIcon({ type, agent, shellLabel, shellTitle, color }: SessionTypeIconProps) {
  return (
    <span style={boxStyle}>
      <ModeIcon type={type} size={16} color={color} />
      {shellLabel ? (
        <span title={shellTitle || shellLabel} style={shellBadgeStyle}>
          {shellLabel}
        </span>
      ) : (
        <span style={agentBadgeStyle}>
          <AgentIcon agentName={agent || ""} style={agentIconStyle} />
        </span>
      )}
    </span>
  );
}

const boxStyle: React.CSSProperties = {
  position: "relative",
  width: "18px",
  height: "18px",
  flexShrink: 0,
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
};

const shellBadgeStyle: React.CSSProperties = {
  position: "absolute",
  right: "-8px",
  bottom: "-4px",
  minWidth: 0,
  maxWidth: "26px",
  height: "auto",
  padding: "1px 3px",
  borderRadius: "5px",
  background: "#1d4ed8",
  border: "none",
  color: "#fff",
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
  fontSize: "7px",
  fontWeight: 700,
  lineHeight: 1.1,
  letterSpacing: 0,
};

const agentBadgeStyle: React.CSSProperties = {
  position: "absolute",
  right: "-2px",
  bottom: "-2px",
  width: "10px",
  height: "10px",
  borderRadius: "999px",
  background: "var(--content-bg, #fff)",
  border: "1px solid rgba(255,255,255,0.9)",
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  overflow: "hidden",
};

const agentIconStyle: React.CSSProperties = {
  width: "10px",
  height: "10px",
  display: "block",
};
