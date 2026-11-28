import React from "react";
import type { AgentStatus } from "../../shared/agentTypes";
import type { SessionStatusExchange, SessionStatusSession } from "../../shared/sessionStatus";
import { selectSessionStatus } from "../../shared/sessionStatus";
import { AgentIcon } from "../agent/AgentIcon";
import { formatModelMeta } from "./sessionViewerFormat";
import { CacheHitBadge, ContextWindowBadge } from "./SessionViewerTokenViews";

export type SessionViewerStatusBarProps = {
  exchanges: readonly SessionStatusExchange[];
  session?: SessionStatusSession | null;
  agents?: AgentStatus[];
  themeColor: string;
};

/**
 * 会话底部常驻状态条：正在用的模型、上下文占用、缓存命中。
 *
 * 它是底部同一行右侧的固定部分：
 * - 位置由 `SessionViewerBottomBar` 的右槽决定（居右、不换行、不随运行状态消失）；
 * - 左侧槽位在「运行中 / 子代理运行中 / 上一轮控件」之间互斥切换，状态条不参与切换；
 * - 数据取最新一条 agent 行（流式期是瞬时行，收尾后由持久行接替），缺字段就隐藏该项；
 *   容器本身常驻，避免「有数据才出现」造成的闪现。
 */
export function SessionViewerStatusBar({
  exchanges,
  session,
  agents,
  themeColor,
}: SessionViewerStatusBarProps) {
  const status = selectSessionStatus(exchanges, session);
  const modelLabel = formatModelMeta(status.model, agents) || status.agent || "";
  return (
    <div
      data-testid="session-status-bar"
      style={{
        display: "flex",
        flexWrap: "nowrap",
        alignItems: "center",
        justifyContent: "flex-end",
        gap: "10px",
        minHeight: "18px",
        minWidth: 0,
        fontSize: "10px",
        color: "var(--text-secondary)",
        opacity: 0.7,
        fontVariantNumeric: "tabular-nums",
      }}
    >
      <span
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: "4px",
          flexShrink: 0,
        }}
      >
        <AgentIcon
          agentName={status.agent || ""}
          style={{ width: "12px", height: "12px", flexShrink: 0, color: themeColor }}
        />
        {modelLabel ? (
          <span
            data-testid="status-model"
            style={{ whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}
          >
            {modelLabel}
          </span>
        ) : null}
      </span>
      {status.contextWindow ? (
        <span data-testid="status-context" style={{ display: "inline-flex", minWidth: 0 }}>
          <ContextWindowBadge contextWindow={status.contextWindow} />
        </span>
      ) : null}
      <CacheHitBadge usage={status.tokenUsage} />
    </div>
  );
}