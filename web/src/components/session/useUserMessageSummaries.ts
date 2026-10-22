import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { TimelineItem } from "../../hooks/useSessionStream";
import { normalizeUserMessageSummary } from "./sessionViewerFormat";

export function useUserMessageSummaries({
  timeline,
  t,
  sessionKey,
  activeScrollRef,
  shouldStickToBottomRef,
  cancelTargetSeqScroll,
  navigation,
}: {
  timeline: TimelineItem[];
  t: (key: any, fallback?: any) => string;
  sessionKey: string | null;
  activeScrollRef: React.RefObject<HTMLDivElement | null>;
  shouldStickToBottomRef: React.RefObject<boolean>;
  cancelTargetSeqScroll: () => void;
  /**
   * 虚拟化导航（CUSTOM(G-JT)）。给出且已启用时，序号定位不再依赖「全部 user 节点都在
   * DOM 里」这个前提 —— 屏外条目根本不挂载，querySelectorAll 只能看到视口附近那几个，
   * 当前序号会永远停在 1。
   */
  navigation?: {
    virtualized: boolean;
    currentUserIndex: () => number;
    scrollToUserIndex: (index: number) => boolean;
  } | null;
}) {
  const [userSummaryHoverOpen, setUserSummaryHoverOpen] = useState(false);
  const [userSummaryPinnedOpen, setUserSummaryPinnedOpen] = useState(false);
  const [currentUserMessageIndex, setCurrentUserMessageIndex] = useState(0);
  const userSummaryRootRef = useRef<HTMLDivElement | null>(null);
  const userSummaryListRef = useRef<HTMLDivElement | null>(null);

  const userMessageSummaries = useMemo(
    () =>
      timeline
        .filter(
          (item): item is TimelineItem & { type: "user_text"; id: string; content: string } =>
            item.type === "user_text",
        )
        .map((item, index) => ({
          id: item.id || `user-${index}`,
          index: index + 1,
          summary: normalizeUserMessageSummary(item.content || "", t("session.emptyMessage")),
        })),
    [timeline, t],
  );
  const userSummaryOpen = userSummaryHoverOpen || userSummaryPinnedOpen;

  const readCurrentUserMessageIndex = useCallback(() => {
    if (navigation?.virtualized) {
      return navigation.currentUserIndex();
    }
    const container = activeScrollRef?.current;
    if (!container) {
      return 0;
    }
    const nodes = Array.from(
      container.querySelectorAll<HTMLElement>("[data-user-message-index]"),
    );
    if (nodes.length === 0) {
      return 0;
    }
    const containerRect = container.getBoundingClientRect();
    const viewportTop = containerRect.top;
    const viewportBottom = containerRect.bottom;
    let firstVisible = 0;
    let firstFullyEntered = 0;
    let lastBeforeViewport = 0;
    for (const node of nodes) {
      const index = Number(node.dataset.userMessageIndex || 0);
      if (!index) {
        continue;
      }
      const rect = node.getBoundingClientRect();
      if (rect.bottom < viewportTop) {
        lastBeforeViewport = index;
        continue;
      }
      if (rect.top > viewportBottom) {
        break;
      }
      const isVisible = rect.bottom >= viewportTop && rect.top <= viewportBottom;
      if (isVisible && !firstVisible) {
        firstVisible = index;
      }
      if (rect.top >= viewportTop && !firstFullyEntered) {
        firstFullyEntered = index;
      }
    }
    return (
      firstFullyEntered ||
      firstVisible ||
      lastBeforeViewport ||
      Number(nodes[0]?.dataset.userMessageIndex || 0)
    );
  }, [activeScrollRef, navigation]);

  const refreshCurrentUserMessageIndex = useCallback(() => {
    setCurrentUserMessageIndex(readCurrentUserMessageIndex());
  }, [readCurrentUserMessageIndex]);

  const scrollToUserMessageSummary = (index: number) => {
    const container = activeScrollRef?.current;
    if (!container) {
      return;
    }
    if (navigation?.virtualized) {
      shouldStickToBottomRef.current = false;
      cancelTargetSeqScroll();
      if (!navigation.scrollToUserIndex(index)) return;
      setUserSummaryPinnedOpen(false);
      setUserSummaryHoverOpen(false);
      setCurrentUserMessageIndex(index);
      return;
    }
    const node = container.querySelector<HTMLElement>(
      `[data-user-message-index="${index}"]`,
    );
    if (!node) {
      return;
    }
    shouldStickToBottomRef.current = false;
    cancelTargetSeqScroll();
    const containerRect = container.getBoundingClientRect();
    const nodeRect = node.getBoundingClientRect();
    const maxTop = Math.max(0, container.scrollHeight - container.clientHeight);
    const nextTop = Math.max(
      0,
      Math.min(
        maxTop,
        container.scrollTop +
          nodeRect.top -
          containerRect.top -
          container.clientHeight / 2 +
          nodeRect.height / 2,
      ),
    );
    container.scrollTop = nextTop;
    setUserSummaryPinnedOpen(false);
    setUserSummaryHoverOpen(false);
    setCurrentUserMessageIndex(index);
  };

  useEffect(() => {
    if (!userSummaryOpen) {
      return;
    }
    const nextIndex = readCurrentUserMessageIndex();
    setCurrentUserMessageIndex(nextIndex);
    if (!nextIndex) {
      return;
    }
    const frame = window.requestAnimationFrame(() => {
      const list = userSummaryListRef.current;
      const item = list?.querySelector<HTMLElement>(
        `[data-user-summary-index="${nextIndex}"]`,
      );
      item?.scrollIntoView({ block: "center" });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [readCurrentUserMessageIndex, userSummaryOpen, userMessageSummaries.length]);

  useEffect(() => {
    if (!userSummaryOpen) {
      return;
    }
    const handlePointerDown = (event: PointerEvent) => {
      const root = userSummaryRootRef.current;
      if (!root || root.contains(event.target as Node)) {
        return;
      }
      setUserSummaryPinnedOpen(false);
      setUserSummaryHoverOpen(false);
    };
    document.addEventListener("pointerdown", handlePointerDown, true);
    return () => document.removeEventListener("pointerdown", handlePointerDown, true);
  }, [userSummaryOpen]);

  useEffect(() => {
    setUserSummaryHoverOpen(false);
    setUserSummaryPinnedOpen(false);
    setCurrentUserMessageIndex(0);
  }, [sessionKey]);

  return {
    userMessageSummaries,
    userSummaryOpen,
    userSummaryRootRef,
    userSummaryListRef,
    currentUserMessageIndex,
    refreshCurrentUserMessageIndex,
    scrollToUserMessageSummary,
    setUserSummaryHoverOpen,
    setUserSummaryPinnedOpen,
  };
}
