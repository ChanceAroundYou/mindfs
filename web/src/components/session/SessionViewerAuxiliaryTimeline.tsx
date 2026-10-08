import React from "react";
import { ToolCallGroupCard } from "../stream/ToolCallGroupCard";
import { TodoUpdateCard, PlanUpdateCard, CompactNoticeCard } from "./SessionViewerCards";
import type { TimelineItem } from "../../hooks/useSessionStream";
import { formatToolCallFallbackResult } from "./sessionViewerFormat";

export function renderAuxiliaryTimelineItem({ item, timelineItemKey, spacing, rootId, rootPath, sessionKey }: { item: Extract<TimelineItem, { type: "tool_group" | "todo" | "plan" | "compact" }>; timelineItemKey: string; spacing: string; rootId?: string | null; rootPath?: string | null; sessionKey: string | null }): React.ReactNode {
// CUSTOM(G-AT): 上游没有 tool_group 这个时间线项类型（逐张渲染 ToolCallCard）。
// 折叠由 useSessionStream 的 groupConsecutiveToolCalls 产生，展开后仍走 ToolCallCard。
if (item.type === "tool_group") {
  return (
    <div key={timelineItemKey} style={{ marginTop: spacing }}>
      <ToolCallGroupCard
        kind={item.kind}
        toolCalls={item.toolCalls}
        rootPath={rootPath || undefined}
        rootId={rootId}
        sessionKey={sessionKey}
        getFallbackResult={formatToolCallFallbackResult}
      />
    </div>
  );
}
if (item.type === "todo") {
  return (
    <div key={timelineItemKey} style={{ marginTop: spacing }}>
      <TodoUpdateCard todoUpdate={item.todoUpdate} />
    </div>
  );
}
if (item.type === "plan") {
  return (
    <div key={timelineItemKey} style={{ marginTop: spacing }}>
      <PlanUpdateCard content={item.planUpdate?.content || ""} rootId={rootId} />
    </div>
  );
}
if (item.type === "compact") {
  return (
    <div key={timelineItemKey} style={{ marginTop: spacing }}>
      <CompactNoticeCard
        status={item.compactNotice?.status}
        summary={item.compactNotice?.summary}
      />
    </div>
  );
}  return null;
}
