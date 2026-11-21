import { useCallback, useEffect, useRef, useState } from "react";
import type { RefObject } from "react";
import { convergeScrollToBottom, distanceFromBottom } from "../common/scrollToBottom";
import { findScrollContainer } from "./conversationScroll";

/**
 * 任务详情面板的「回到底部」。
 *
 * 收敛复用会话时间线那份（components/common/scrollToBottom.ts）：点击后逐帧写
 * maxTop 到真的到底。差异只在于滚动容器是 `.mindfs-panel-body`（对话、执行详情、
 * 完成证据共用的那一个），所以这里从对话末尾的锚点往上找，而不是拿 window 或
 * 某个固定 ref。
 *
 * 打开详情时的自动落底在 ConversationView 里单独做一次；本 hook 只负责之后
 * 用户自己上翻时把按钮亮出来，两者不共享状态，也不会互相抢滚动位置。
 */

/**
 * 任务详情的显隐阈值（与会话页的 STICK_REARM_PX=2 有意不同）。
 *
 * 会话页输入框是浮在滚动区上的固定层，按钮永远在它上方，2px 就够。任务详情的输入框
 * 在滚动流里、就在对话末尾，面板底部还有 12–46px 的内边距与收尾区，2px 会让按钮在
 * 「刚打开、刚落到最新消息」时就亮着，紧贴输入框时两个控件还会重叠。200px 是「已经
 * 明显上翻、输入框已经滚出视口」的距离；收敛行为仍然完全共用会话那一份。
 */
export const JUMP_VISIBLE_PX = 200;

export function useConversationJumpToLatest(
  anchorRef: RefObject<HTMLElement | null>,
  resetKey: string,
): { visible: boolean; jumpToLatest: () => void } {
  const [visible, setVisible] = useState(false);
  const scrollerRef = useRef<HTMLElement | null>(null);
  const cancelRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    setVisible(false);
    const anchor = anchorRef.current;
    if (!anchor) return;

    const scroller = findScrollContainer(anchor);
    if (!scroller) return;
    scrollerRef.current = scroller;

    const update = () => setVisible(distanceFromBottom(scroller) > JUMP_VISIBLE_PX);
    // 收敛期间用户自己上手 → 立刻让位，不和手势抢位置（会话侧的同一约定）。
    const cancelScroll = () => cancelRef.current?.();
    const onWheel = (event: WheelEvent) => {
      if (event.deltaY < 0) cancelScroll();
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "PageUp" || event.key === "ArrowUp" || event.key === "Home") cancelScroll();
    };

    scroller.addEventListener("scroll", update, { passive: true });
    scroller.addEventListener("wheel", onWheel, { passive: true });
    scroller.addEventListener("touchmove", cancelScroll, { passive: true });
    scroller.addEventListener("keydown", onKeyDown);
    // 内容可能在首屏之后才撑开（异步执行详情），每帧读一次位置直到稳定。
    update();
    return () => {
      cancelRef.current?.();
      cancelRef.current = null;
      scrollerRef.current = null;
      scroller.removeEventListener("scroll", update);
      scroller.removeEventListener("wheel", onWheel);
      scroller.removeEventListener("touchmove", cancelScroll);
      scroller.removeEventListener("keydown", onKeyDown);
    };
  }, [anchorRef, resetKey]);

  const jumpToLatest = useCallback(() => {
    const scroller = scrollerRef.current;
    if (!scroller) return;
    cancelRef.current?.();
    setVisible(false);
    cancelRef.current = convergeScrollToBottom(scroller);
  }, []);

  return { visible, jumpToLatest };
}
