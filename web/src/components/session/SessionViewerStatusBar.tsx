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
 * 与轮次控件行（复制 / fork / 完成时间）分开：
 * - 控件行只在**已完整结束**的轮次后出现（见 SessionViewerTimeline）；
 * - 状态条描述会话当前状态，运行中也显示、实时更新、不随轮次重复。
 *
 * 数据取最新一条 agent 行（流式期是瞬时行，收尾后由持久行接替），缺字段就隐藏该项；
 * 容器本身常驻，避免「有数据才出现」造成的闪现。
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
        flexWrap: "wrap",
        alignItems: "center",
        gap: "10px",
        width: "100%",
        minHeight: "18px",
        padding: "6px 2px 2px",
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
        {modelLabel ? <span data-testid="status-model">{modelLabel}</span> : null}
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