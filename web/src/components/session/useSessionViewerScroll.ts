import { useEffect, useRef, useState } from "react";
import type { RefObject, MutableRefObject, Dispatch, SetStateAction } from "react";

export function useSessionViewerScrollState(activeScrollRef: RefObject<HTMLDivElement | null>) {
  const shouldStickToBottomRef = useRef(true);
  const lastSessionKeyRef = useRef<string | null>(null);
  const targetSeqScrollKeyRef = useRef("");
  const targetSeqFrameRef = useRef<number | null>(null);
  const targetSeqTimerRefs = useRef<number[]>([]);
  const [showJumpToLatest, setShowJumpToLatest] = useState(false);
  const viewportStickFrameRef = useRef<number | null>(null);

  const cancelTargetSeqScroll = () => {
    if (targetSeqFrameRef.current !== null) {
      window.cancelAnimationFrame(targetSeqFrameRef.current);
      targetSeqFrameRef.current = null;
    }
    targetSeqTimerRefs.current.forEach((timer) => window.clearTimeout(timer));
    targetSeqTimerRefs.current = [];
  };

  const stickSessionToBottom = (behavior: ScrollBehavior = "auto") => {
    const container = activeScrollRef?.current;
    if (!container) {
      return;
    }
    const maxTop = Math.max(0, container.scrollHeight - container.clientHeight);
    container.scrollTo({ top: maxTop, behavior });
  };

  return {
    shouldStickToBottomRef, lastSessionKeyRef, targetSeqScrollKeyRef,
    targetSeqFrameRef, targetSeqTimerRefs, showJumpToLatest,
    setShowJumpToLatest, viewportStickFrameRef, cancelTargetSeqScroll,
    stickSessionToBottom,
  };
}

export function useSessionViewerScrollPositionEffects({
  activeScrollRef, interactionMode, isPrependingRef, lastSessionKeyRef,
  scrollEndRef, scrollToRelatedFilesDividerRef, sessionKey, timeline,
  isStreaming, streamVersion, slashCommandResult, shouldStickToBottomRef,
  viewportStickFrameRef, stickSessionToBottom,
}: {
  activeScrollRef: RefObject<HTMLDivElement | null>;
  interactionMode: "main" | "drawer";
  isPrependingRef: MutableRefObject<boolean>;
  lastSessionKeyRef: MutableRefObject<string | null>;
  scrollEndRef: RefObject<HTMLDivElement | null>;
  scrollToRelatedFilesDividerRef: RefObject<(behavior?: ScrollBehavior) => void>;
  sessionKey: string | null;
  timeline: unknown[];
  isStreaming: boolean;
  streamVersion: number;
  slashCommandResult: unknown;
  shouldStickToBottomRef: MutableRefObject<boolean>;
  viewportStickFrameRef: MutableRefObject<number | null>;
  stickSessionToBottom: (behavior?: ScrollBehavior) => void;
}) {
  useEffect(() => {
    if (isPrependingRef.current) {
      // 顶部插入（loadMore 历史段）不触发底部跟随，视口由 loadMore 的 rAF 回补。
      return;
    }
    const container = activeScrollRef?.current;
    if (!container) {
      if (interactionMode === "drawer" && shouldStickToBottomRef.current) {
        const frame = window.requestAnimationFrame(() => {
          const retry = activeScrollRef?.current;
          if (retry && shouldStickToBottomRef.current) scrollToRelatedFilesDividerRef.current("auto");
        });
        return () => window.cancelAnimationFrame(frame);
      }
      return;
    }
    if (!scrollEndRef.current) {
      return;
    }
    const nextKey = sessionKey;
    const isSessionChanged = lastSessionKeyRef.current !== nextKey;
    if (isSessionChanged) {
      lastSessionKeyRef.current = nextKey;
      shouldStickToBottomRef.current = true;
    }
    if (shouldStickToBottomRef.current) {
      scrollToRelatedFilesDividerRef.current("auto");
    }
  }, [sessionKey, timeline, isStreaming, streamVersion, slashCommandResult]);

  useEffect(() => {
    const container = activeScrollRef?.current;
    if (!container || typeof window === "undefined") {
      return;
    }
    const queueStickToBottom = () => {
      if (!shouldStickToBottomRef.current) {
        return;
      }
      if (viewportStickFrameRef.current !== null) {
        window.cancelAnimationFrame(viewportStickFrameRef.current);
      }
      viewportStickFrameRef.current = window.requestAnimationFrame(() => {
        viewportStickFrameRef.current = null;
        if (!shouldStickToBottomRef.current) {
          return;
        }
        stickSessionToBottom("auto");
      });
    };
    window.visualViewport?.addEventListener("resize", queueStickToBottom);
    window.visualViewport?.addEventListener("scroll", queueStickToBottom);
    window.addEventListener("mindfs:safe-area-updated", queueStickToBottom as EventListener);
    return () => {
      window.visualViewport?.removeEventListener("resize", queueStickToBottom);
      window.visualViewport?.removeEventListener("scroll", queueStickToBottom);
      window.removeEventListener("mindfs:safe-area-updated", queueStickToBottom as EventListener);
      if (viewportStickFrameRef.current !== null) {
        window.cancelAnimationFrame(viewportStickFrameRef.current);
        viewportStickFrameRef.current = null;
      }
    };
  }, [sessionKey]);


}

export function useSessionViewerStickinessEffect({
  activeScrollRef, refreshCurrentUserMessageIndex, sessionKey,
  shouldStickToBottomRef, setShowJumpToLatest,
}: {
  activeScrollRef: RefObject<HTMLDivElement | null>;
  refreshCurrentUserMessageIndex: () => void;
  sessionKey: string | null;
  shouldStickToBottomRef: MutableRefObject<boolean>;
  setShowJumpToLatest: Dispatch<SetStateAction<boolean>>;
}) {
  useEffect(() => {
    const el = activeScrollRef?.current;
    if (!el) {
      shouldStickToBottomRef.current = true;
      setShowJumpToLatest(false);
      return;
    }
    let lastScrollTop = el.scrollTop;
    // CUSTOM(G-AU): 滚动事件原先**每个事件**都跑一遍 refreshCurrentUserMessageIndex
    // （querySelectorAll 全部 user 节点 + 逐个 getBoundingClientRect + setState），
    // 没有任何节流 —— 在「往上翻历史」这条路径上就是连续的主线程阻塞。
    // 合并到一帧一次：滚动体感不变，但 DOM 查询从「每事件」降到「每帧」。
    let stickinessFrame: number | null = null;
    const updateStickiness = () => {
      if (stickinessFrame !== null) return;
      stickinessFrame = window.requestAnimationFrame(() => {
        stickinessFrame = null;
        const viewportGap = window.visualViewport
          ? window.innerHeight - window.visualViewport.height - window.visualViewport.offsetTop
          : 0;
        const rawDistanceFromBottom = el.scrollHeight - el.clientHeight - el.scrollTop;
        const distanceFromBottom = Math.max(0, rawDistanceFromBottom - viewportGap);
        const isNearBottom = distanceFromBottom < 40;
        const movedUp = el.scrollTop < lastScrollTop;
        const movedDown = el.scrollTop > lastScrollTop;
        if (isNearBottom) {
          shouldStickToBottomRef.current = true;
        } else if (movedUp) {
          shouldStickToBottomRef.current = false;
        } else if (movedDown && distanceFromBottom < 200) {
          shouldStickToBottomRef.current = true;
        }
        setShowJumpToLatest(!shouldStickToBottomRef.current);
        refreshCurrentUserMessageIndex();
        lastScrollTop = el.scrollTop;
      });
    };
    updateStickiness();
    el.addEventListener("scroll", updateStickiness, { passive: true });
    return () => {
      if (stickinessFrame !== null) {
        window.cancelAnimationFrame(stickinessFrame);
        stickinessFrame = null;
      }
      el.removeEventListener("scroll", updateStickiness);
    };
  }, [refreshCurrentUserMessageIndex, sessionKey]);
}
