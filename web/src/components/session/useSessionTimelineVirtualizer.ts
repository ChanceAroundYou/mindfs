import { useCallback, useMemo, useRef } from "react";
import type { RefObject } from "react";
import { defaultRangeExtractor, useVirtualizer, type Virtualizer } from "@tanstack/react-virtual";
import type { TimelineItem } from "../../hooks/useSessionStream";
import type { SessionViewerTimelineVirtual } from "./SessionViewerTimeline";

// 时间线虚拟化（CUSTOM(G-JT)）。
//
// 起因（2026-10-22，pc 节点 / AIS核心算法 / 「重新训练」真机实测）：打开会话时
// 642 张思考卡 + ~640 张工具卡**同帧**挂载 —— DOM 941 → 16,824，一次 1.4–1.9 s
// 主线程长任务。窗口预算（G-JS）把载荷压下去了，但「一页仍是上千个条目」这件事
// 本身没变：只要条目数超过视口容量，全量挂载必然是一次长任务。虚拟化是这条路径上
// 唯一与会话内容无关的解法 —— 屏外条目不挂载。
//
// 为什么单独成 hook 而不是写进 SessionViewer：seq→index 与用户消息序号有三个消费者
// （时间线渲染、用户消息摘要导航、targetSeq 跳转），它们必须看**同一份**索引；
// 且 SessionViewer 已在硬线（500 行）边缘，不该再长。

/** 低于这个条目数不启用虚拟化：少条目全量渲染更快（省掉测量与占位高度）。 */
export const TIMELINE_VIRTUALIZE_MIN_ITEMS = 48;
export const TIMELINE_OVERSCAN = 8;

/** 估算行高。折叠态的工具卡/思考卡都是一行标题（实测 ~40px），正文行更高。 */
function estimateSize(item: TimelineItem | undefined): number {
  switch (item?.type) {
    case "tool":
    case "tool_group":
    case "thought":
      return 40;
    case "user_text":
    case "assistant_text":
      return 160;
    default:
      return 72;
  }
}

export type TimelineNavigation = {
  virtualized: boolean;
  /** 传进 SessionViewerTimeline 的 `virtual`；未虚拟化时为 null（全量渲染）。 */
  virtual: SessionViewerTimelineVirtual | null;
  /** 视口顶部对应的用户消息序号（1 起）；未虚拟化时返回 0，调用方走 DOM 路径。 */
  currentUserIndex: () => number;
  /** 滚到第 index 条用户消息（1 起）。未虚拟化时返回 false。 */
  scrollToUserIndex: (index: number) => boolean;
  /** 滚到某个持久 seq。未虚拟化 / 不在时间线内时返回 false。 */
  scrollToSeq: (seq: number) => boolean;
};

export function useSessionTimelineVirtualizer({
  timeline,
  timelineItemMeta,
  scrollRef,
  sessionKey,
}: {
  timeline: TimelineItem[];
  timelineItemMeta: Array<{ userIndex: number }>;
  scrollRef: RefObject<HTMLDivElement | null>;
  sessionKey: string | null;
}): TimelineNavigation {
  // buildTimelineItemMeta 只在 user 行写累计序号，非 user 行是 0；
  // 虚拟化需要「任意条目 i 的累计用户消息数」，这里补成单调序列。
  const userIndexAt = useMemo(() => {
    let users = 0;
    return timelineItemMeta.map((meta, i) => {
      if (timeline[i]?.type === "user_text") users = meta.userIndex;
      return users;
    });
  }, [timelineItemMeta, timeline]);
  const seqToIndex = useMemo(() => {
    const map = new Map<number, number>();
    const userIndices: number[] = [];
    const pendingAsk: number[] = [];
    for (let i = 0; i < timeline.length; i += 1) {
      const item = timeline[i];
      if (item.type === "user_text") userIndices.push(i);
      if ("seq" in item) {
        const seq = Number(item.seq || 0);
        if (seq > 0 && !map.has(seq)) map.set(seq, i);
      }
      // 未答 ask 卡必须常驻：草稿就在它的 DOM 里，滚走即丢（与旧实现同一条约束）。
      if (
        item.type === "tool" &&
        `${item.toolCall?.kind || ""}`.toLowerCase() === "ask_user" &&
        ["running", "pending", "in_progress"].includes(
          `${item.toolCall?.status || ""}`.toLowerCase(),
        )
      ) {
        pendingAsk.push(i);
      }
    }
    return { map, userIndices, pendingAsk };
  }, [timeline]);

  const virtualized = timeline.length >= TIMELINE_VIRTUALIZE_MIN_ITEMS;
  const scrollRefBox = useRef(scrollRef);

  const virtualizer = useVirtualizer({
    count: timeline.length,
    getScrollElement: () => scrollRefBox.current.current,
    enabled: virtualized,
    getItemKey: (i: number) => `${sessionKey || ""}:${timeline[i]?.id || i}`,
    estimateSize: (i: number) => estimateSize(timeline[i]),
    overscan: TIMELINE_OVERSCAN,
    shouldAdjustScrollPositionOnItemSizeChange: (_item: any, _delta: any, instance: any) =>
      instance.scrollDirection !== "backward",
    rangeExtractor: (range: any) =>
      Array.from(new Set([...defaultRangeExtractor(range), ...seqToIndex.pendingAsk])).sort(
        (a, b) => a - b,
      ),
  } as any);

  const currentUserIndex = useCallback(() => {
    if (!virtualized) return 0;
    const items = virtualizer.getVirtualItems();
    if (items.length === 0) return 0;
    const offset = virtualizer.scrollOffset ?? 0;
    // 视口顶部那一条：第一个「底边还没被卷过去」的条目。
    const top = items.find((entry) => entry.end >= offset) ?? items[0];
    return Math.max(1, userIndexAt[top.index] || 0);
  }, [userIndexAt, virtualized, virtualizer]);

  const scrollToUserIndex = useCallback(
    (userIndex: number) => {
      if (!virtualized) return false;
      const target = seqToIndex.userIndices[userIndex - 1];
      if (target === undefined) return false;
      virtualizer.scrollToIndex(target, { align: "center" });
      return true;
    },
    [seqToIndex.userIndices, virtualized, virtualizer],
  );

  const scrollToSeq = useCallback(
    (seq: number) => {
      if (!virtualized) return false;
      const target = seqToIndex.map.get(seq);
      if (target === undefined) return false;
      virtualizer.scrollToIndex(target, { align: "center" });
      return true;
    },
    [seqToIndex.map, virtualized, virtualizer],
  );

  return {
    virtualized,
    virtual: virtualized
      ? {
          items: virtualizer.getVirtualItems(),
          totalSize: virtualizer.getTotalSize(),
          measureElement: virtualizer.measureElement,
        }
      : null,
    currentUserIndex,
    scrollToUserIndex,
    scrollToSeq,
  };
}
