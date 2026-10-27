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
  const bottomConvergeFrameRef = useRef<number | null>(null);

  const cancelTargetSeqScroll = () => {
    if (targetSeqFrameRef.current !== null) {
      window.cancelAnimationFrame(targetSeqFrameRef.current);
      targetSeqFrameRef.current = null;
    }
    targetSeqTimerRefs.current.forEach((timer) => window.clearTimeout(timer));
    targetSeqTimerRefs.current = [];
  };

  // 逐帧收敛到底（G-KB 回归修复）。
  //
  // 曾经是一次 `scrollTo({ behavior: "smooth" })`。虚拟化会话里每帧都可能有新行挂载
  // 把 scrollHeight 撑大，而虚拟库在「向下」方向的测量修正会**即时**写 scrollTop ——
  // 一次即时写入就取消浏览器进行中的 smooth 动画，于是按钮经常停在半路。
  // 改成每帧写 maxTop，直到连续几帧没有任何位移：新撑出来的高度下一帧自动补上。
  //
  // 参数 behavior 仅为调用方签名兼容保留，实际一律即时写（精准到底优先于动画）。
  const stickSessionToBottom = (_behavior: ScrollBehavior = "auto") => {
    const container = activeScrollRef?.current;
    if (!container) {
      return;
    }
    if (bottomConvergeFrameRef.current !== null) {
      window.cancelAnimationFrame(bottomConvergeFrameRef.current);
      bottomConvergeFrameRef.current = null;
    }
    let stableFrames = 0;
    let frames = 0;
    const MAX_FRAMES = 120;
    // 最少跑 8 帧：新挂载行的实测（ResizeObserver）往往比写 scrollTop 晚一两帧落地，
    // 只按「连续稳定」判定会在测量到达之前就收工，停在估算高度算出来的位置。
    const MIN_FRAMES = 8;
    const step = () => {
      bottomConvergeFrameRef.current = null;
      const el = activeScrollRef?.current;
      if (!el) return;
      // 收敛期间用户上滚 → 立刻让位，绝不和手势抢位置。
      if (!shouldStickToBottomRef.current) return;
      const maxTop = Math.max(0, el.scrollHeight - el.clientHeight);
      const gap = maxTop - el.scrollTop;
      if (gap > 1) {
        el.scrollTop = maxTop;
        stableFrames = 0;
      } else {
        stableFrames += 1;
      }
      frames += 1;
      if ((frames >= MIN_FRAMES && stableFrames >= 3) || frames >= MAX_FRAMES) return;
      bottomConvergeFrameRef.current = window.requestAnimationFrame(step);
    };
    bottomConvergeFrameRef.current = window.requestAnimationFrame(step);
  };

  useEffect(() => {
    return () => {
      if (bottomConvergeFrameRef.current !== null) {
        window.cancelAnimationFrame(bottomConvergeFrameRef.current);
        bottomConvergeFrameRef.current = null;
      }
    };
  }, []);

  return {
    shouldStickToBottomRef, lastSessionKeyRef, targetSeqScrollKeyRef,
    targetSeqFrameRef, targetSeqTimerRefs, showJumpToLatest,
    setShowJumpToLatest, viewportStickFrameRef, cancelTargetSeqScroll,
    stickSessionToBottom,
  };
}

export function useSessionViewerScrollPositionEffects({
  activeScrollRef, interactionMode, isPrependingRef, lastSessionKeyRef,
  scrollEndRef, scrollToRelatedFilesDividerRef, sessionKey, timelineLength,
  timelineTailId, isStreaming, streamVersion, slashCommandResult, shouldStickToBottomRef,
  viewportStickFrameRef, stickSessionToBottom,
}: {
  activeScrollRef: RefObject<HTMLDivElement | null>;
  interactionMode: "main" | "drawer";
  isPrependingRef: MutableRefObject<boolean>;
  lastSessionKeyRef: MutableRefObject<string | null>;
  scrollEndRef: RefObject<HTMLDivElement | null>;
  scrollToRelatedFilesDividerRef: RefObject<(behavior?: ScrollBehavior) => void>;
  sessionKey: string | null;
  timelineLength: number;
  timelineTailId: string;
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
  }, [sessionKey, timelineLength, timelineTailId, isStreaming, streamVersion, slashCommandResult]);

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
        // 2px 以内才认为"在底部"：小幅上滚不应被重新判定为跟随。
        // movedDown 也不再重新吸底 —— 只有回到底部（<=2px）才恢复跟随。
        const STICK_REARM_PX = 2;
        const isNearBottom = distanceFromBottom <= STICK_REARM_PX;
        const movedUp = el.scrollTop < lastScrollTop;
        if (isNearBottom) {
          shouldStickToBottomRef.current = true;
        } else if (movedUp) {
          shouldStickToBottomRef.current = false;
        }
        // 按钮显隐只跟「现在离底多远」有关，与「是否跟随」解耦：
        // 后者是「追加内容时要不要自动滚」的意图，前者是位置的直接读数。
        // 曾经用 !shouldStickToBottomRef 派生，于是滚轮下到底、拖动滚动条上滑
        // 这两种不产生显式手势状态变化的路径都不更新按钮（2026-10-27 报障）。
        setShowJumpToLatest(distanceFromBottom > STICK_REARM_PX);
        refreshCurrentUserMessageIndex();
        lastScrollTop = el.scrollTop;
      });
    };
    // 手势只负责「让位」（停跟随），不再碰按钮显隐 —— 单一真相源在 updateStickiness。
    const onWheel = (event: WheelEvent) => {
      if (event.deltaY < 0) {
        shouldStickToBottomRef.current = false;
      }
    };
    const onTouchMove = (event: TouchEvent) => {
      // touchmove 向上滚动：y 增大
      const touch = event.touches?.[0];
      if (!touch) return;
      const prev = (el as any).__lastTouchY;
      if (typeof prev === "number" && touch.clientY > prev) {
        shouldStickToBottomRef.current = false;
      }
      (el as any).__lastTouchY = touch.clientY;
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (["PageUp", "ArrowUp", "Home"].includes(event.key)) {
        shouldStickToBottomRef.current = false;
      }
    };
    // scroll 监听是按钮显隐的唯一输入：丢了它，滚轮下到底不消失、拖滚动条上滑不出现
    // （2026-10-27 实证：b24dbee3 里 removeEventListener 留着、addEventListener 被删了，
    // 只剩三个「只会置真」的手势，按钮于是只能靠点击回写）。
    updateStickiness();
    el.addEventListener("scroll", updateStickiness, { passive: true });
    el.addEventListener("wheel", onWheel, { passive: true });
    el.addEventListener("touchmove", onTouchMove, { passive: true });
    el.addEventListener("keydown", onKeyDown);
    return () => {
      if (stickinessFrame !== null) {
        window.cancelAnimationFrame(stickinessFrame);
        stickinessFrame = null;
      }
      el.removeEventListener("scroll", updateStickiness);
      el.removeEventListener("wheel", onWheel);
      el.removeEventListener("touchmove", onTouchMove);
      el.removeEventListener("keydown", onKeyDown);
    };
  }, [refreshCurrentUserMessageIndex, sessionKey]);
}
