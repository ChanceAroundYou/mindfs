import React from "react";
import { AgentIcon } from "../agent/AgentIcon";
import { ModeIcon } from "../agent/ModeIcon";
import { renderToolIcon } from "../stream/toolCallIcons";
import { taskAuxBadgeStyle, taskAuxIconScaleStyle } from "../task/taskIcons";
import { useI18n } from "../../i18n/index";
import { shellBadgeLabel } from "./sessionListShared";
import { SubSessionIcon } from "./sessionCardHelpers";
import type { SessionItem } from "./sessionListTypes";

/**
 * 会话行最左侧的图标位（G-AD）：模式图标 + 右下角角标（等回答的问号 / shell / agent 圆点）。
 * 2026-10-16 从 SessionCard 整块抽出（原文 110–185 行），纯搬移、零行为变化 ——
 * 抽出来只为行数门禁：SessionCard 当时 492 行，离 500 的硬线只剩 8 行。
 *
 * 18px 的定位盒是角标的基准：三个角标都是 position:absolute 挂在右下角，
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
  const { t } = useI18n();
  if (isSearchResult) {
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
      {isSubagent ? (
        <SubSessionIcon
          color={nodeColor ? `color-mix(in srgb, ${nodeColor} 78%, var(--text-secondary))` : undefined}
        />
      ) : (
        <ModeIcon
          type={session.task_id ? "task" : session.type || "chat"}
          size={16}
          color={nodeColor || undefined}
        />
      )}
      {!isSubagent && session.aux_flags?.ask_user_waiting ? (
        <span
          aria-label={t("sessionList.awaitingUser")}
          title={t("sessionList.awaitingUser")}
          style={taskAuxBadgeStyle(true)}
        >
          <span style={taskAuxIconScaleStyle()}>{renderToolIcon("ask_user")}</span>
        </span>
      ) : null}
      {!isSubagent && session.type === "command" ? (
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
      ) : !isSubagent ? (
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
      ) : null}
    </span>
  );
}
