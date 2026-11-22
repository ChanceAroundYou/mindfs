import { useEffect } from "react";
import type { RefObject } from "react";
import { distanceFromBottom } from "../common/scrollToBottom";
import { alignPanelToBottom, findScrollContainer } from "./conversationScroll";

/** 距底部不超过这个像素数就算「贴在底部」，输入区长高时跟着往上顶。 */
export const COMPOSER_PIN_PX = 48;

/**
 * 输入区变高时把面板底部钉住。
 *
 * 任务详情不是独立滚动区：对话、输入框、下方两个胶囊都在同一个
 * `.mindfs-panel-body` 里。用户在输入框里敲出新的一行时，输入区跟着长高，
 * scrollTop 不变 → 下面的胶囊被顶出视口（实测桌面端胶囊直接消失）。
 *
 * 这里观察输入区容器的高度：只要变高时滚动条本来就贴着底部，就重新对齐到面板
 * 底部 —— 视觉上对话内容被往上顶，输入框与胶囊原地不动。用户上翻历史时不抢
 * 滚动位置（`pinned` 由滚动事件维护），与他自己的手势一致。
 */
export function useComposerBottomPin(
  composerRef: RefObject<HTMLElement | null>,
  anchorRef: RefObject<HTMLElement | null>,
) {
  useEffect(() => {
    const composer = composerRef.current;
    const anchor = anchorRef.current;
    if (!composer || !anchor || typeof ResizeObserver === "undefined") return;
    const scroller = findScrollContainer(anchor);
    if (!scroller) return;

    let pinned = distanceFromBottom(scroller) <= COMPOSER_PIN_PX;
    let lastHeight = composer.getBoundingClientRect().height;
    const onScroll = () => {
      pinned = distanceFromBottom(scroller) <= COMPOSER_PIN_PX;
    };
    scroller.addEventListener("scroll", onScroll, { passive: true });
    const observer = new ResizeObserver(() => {
      const height = composer.getBoundingClientRect().height;
      const delta = height - lastHeight;
      lastHeight = height;
      if (!delta || !pinned) return;
      alignPanelToBottom(anchor);
    });
    observer.observe(composer);
    return () => {
      observer.disconnect();
      scroller.removeEventListener("scroll", onScroll);
    };
  }, [anchorRef, composerRef]);
}
