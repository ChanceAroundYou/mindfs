import React from "react";
import { ThinkingBlock } from "../stream/ThinkingBlock";
import type { TimelineItem } from "../../hooks/useSessionStream";

export function SessionViewerThoughtItem({
  item,
  timelineItemKey,
  spacing,
}: {
  item: Extract<TimelineItem, { type: "thought" }>;
  timelineItemKey: string;
  spacing: string;
}) {
  return (
    <div key={timelineItemKey} style={{ marginTop: spacing }}>
      <ThinkingBlock content={item.content || ""} defaultExpanded={false} />
    </div>
  );
}
