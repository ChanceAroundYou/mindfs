import React from "react";
import { AgentIcon } from "../agent/AgentIcon";
import { ModeIcon } from "../agent/ModeIcon";
import { shellBadgeLabel } from "./sessionListShared";
import type { SessionItem } from "./sessionListTypes";

/**
 * 会话行最左侧的图标位（G-AD）：会话类型图标（ModeIcon）+ 右下角角标（shell / agent 圆点）。
 * 2026-10-16 从 SessionCard 整块抽出（原文 110–185 行），纯搬移、零行为变化 ——
 * 抽出来只为行数门禁：SessionCard 当时 492 行，离 500 的硬线只剩 8 行。
 *
 * 2026-10-16 二次收窄：子代理图标与等回答的问号**搬到会话名上方那排**去了
 * （SessionCardTopBadges，用户要求「小状态图标都放到名字上面，会话行下面不留东西」）。
 * 剩下这两个角标是「这条会话是什么」的常驻标识（跑在哪个 agent / 哪个 shell），
 * 不是状态，留在原位。
 *
 * 18px 的定位盒是角标的基准：两个角标都是 position:absolute 挂在右下角，
 * 所以盒子尺寸不能随手改，否则角标跟着漂。
 * 子代理行整格不渲染 —— 它的标记已经在名字上方，这里再放一个就是同一个信号响两遍；
 * 行缩进由 SessionCard 的 paddingLeft 保住。
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
  if (isSearchResult || isSubagent) {
    return null;
  }
  return (
    <span
      style={{
        position: "relative",
        width: "18px",
        height: "18px",
        flexShrink: 0,
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
      }}
    >
      <ModeIcon
        type={session.task_id ? "task" : session.type || "chat"}
        size={16}
        color={nodeColor || undefined}
      />
      {session.type === "command" ? (
        <span
          title={session.shell || "shell"}
          style={{
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
          }}
        >
          {shellBadgeLabel(session.shell)}
        </span>
      ) : (
        <span
          style={{
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
          }}
        >
          <AgentIcon
            agentName={session.agent || ""}
            style={{ width: "10px", height: "10px", display: "block" }}
          />
        </span>
      )}
    </span>
  );
}
