import React from "react";
import { SessionTypeIcon } from "../common/SessionTypeIcon";
import { shellBadgeLabel } from "./sessionListShared";
import { SubSessionIcon } from "./sessionCardHelpers";
import type { SessionItem } from "./sessionListTypes";

/**
 * 会话行最左侧的图标位（G-AD）：会话类型图标（ModeIcon）+ 右下角角标（shell / agent 圆点）。
 * 2026-10-16 从 SessionCard 整块抽出（原文 110–185 行），纯搬移、零行为变化 ——
 * 抽出来只为行数门禁：SessionCard 当时 492 行，离 500 的硬线只剩 8 行。
 *
 * 子代理的折角是「这条会话是什么」的常驻标识，不是运行状态：它固定放在名字前；
 * 问号与其它状态徽标仍归 SessionCardTopBadges。子代理没有状态徽标排。
 *
 * 18px 的定位盒是角标的基准：两个角标都是 position:absolute 挂在右下角，
 * 所以盒子尺寸不能随手改，否则角标跟着漂。
 */
export function SessionCardIcon({
  session,
  isSubagent,
  isSearchResult,
  nodeColor,
}: {
  session: SessionItem;
  isSubagent: boolean;
  isSearchResult: boolean;
  nodeColor?: string;
}) {
  if (isSearchResult) {
    return null;
  }
  if (isSubagent) {
    return (
      <span
        style={{
          width: "18px",
          height: "18px",
          flexShrink: 0,
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
        }}
      >
        <SubSessionIcon
          color={nodeColor ? `color-mix(in srgb, ${nodeColor} 78%, var(--text-secondary))` : undefined}
        />
      </span>
    );
  }
  return (
    <SessionTypeIcon
      type={session.task_id ? "task" : session.type || "chat"}
      agent={session.agent}
      color={nodeColor || undefined}
      shellLabel={session.type === "command" ? shellBadgeLabel(session.shell) : undefined}
      shellTitle={session.type === "command" ? session.shell || "shell" : undefined}
    />
  );
}
