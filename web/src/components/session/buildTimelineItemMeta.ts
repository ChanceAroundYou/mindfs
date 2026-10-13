export function buildTimelineItemMeta(timeline: Array<{ type?: string; timestamp?: number | string } | undefined>) {
    const metas = new Array<{
      userIndex: number;
      prevUserTs: string | undefined;
    }>(timeline.length);
    let userCount = 0;
    let lastUserTs: string | undefined;
    for (let i = 0; i < timeline.length; i += 1) {
      const entry = timeline[i] as { type?: string; timestamp?: number | string } | undefined;
      const isUser = entry?.type === "user_text";
      if (isUser) {
        userCount += 1;
        lastUserTs = entry?.timestamp ? String(entry.timestamp) : undefined;
      }
      metas[i] = {
        userIndex: isUser ? userCount : 0,
        prevUserTs: isUser ? undefined : lastUserTs,
      };
    }
    return metas;
}
