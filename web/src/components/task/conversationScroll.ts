// 打开任务详情时把对话滚到最新消息。
//
// 任务详情不是独立滚动区：`.mindfs-panel-body` 是唯一滚动容器（对话、执行详情
// 胶囊、完成证据都在里面）。所以这里从对话末尾的锚点往上找最近的可滚动祖先，
// 再把「锚点底部」对齐到该容器的可视底部。
//
// 只负责几何计算与一次对齐，不关心何时调用（视图层在首屏数据到达后调用）。

export type BottomAlignInput = {
  /** 容器当前滚动位置。 */
  scrollTop: number;
  scrollHeight: number;
  clientHeight: number;
  /** 锚点与容器的视口坐标（getBoundingClientRect 的 bottom）。 */
  anchorBottom: number;
  scrollerBottom: number;
};

/**
 * 把锚点底部对齐到容器底部所需的 scrollTop，并夹在 [0, 最大滚动] 之间。
 *
 * 纯函数：内容还没铺满（最大滚动为 0）时返回 0；锚点已经在可视区底部时
 * 返回当前值，不会把用户往下多滚一截。
 */
export function bottomAlignedScrollTop(input: BottomAlignInput): number {
  const max = Math.max(0, input.scrollHeight - input.clientHeight);
  const target = input.scrollTop + (input.anchorBottom - input.scrollerBottom);
  return Math.max(0, Math.min(target, max));
}

/**
 * 从锚点向上找最近的可滚动祖先（overflowY auto/scroll 且内容高于可视区）。
 * 找不到返回 null —— 调用方什么都不做，绝不去滚 window。
 */
export function findScrollableAncestor(start: HTMLElement): HTMLElement | null {
  let node: HTMLElement | null = start.parentElement;
  while (node) {
    const style = window.getComputedStyle(node);
    const scrollable = style.overflowY === "auto" || style.overflowY === "scroll";
    if (scrollable && node.scrollHeight > node.clientHeight + 1) return node;
    node = node.parentElement;
  }
  return null;
}

/**
 * 把对话末尾对齐到最近的可滚动祖先底部；返回该容器（找不到时为 null）。
 * 对齐后再次调用是幂等的，视图层可以在布局稳定的头几帧里重复调用。
 */
export function alignConversationToBottom(anchor: HTMLElement): HTMLElement | null {
  const scroller = findScrollableAncestor(anchor);
  if (!scroller) return null;
  const anchorRect = anchor.getBoundingClientRect();
  const scrollerRect = scroller.getBoundingClientRect();
  scroller.scrollTop = bottomAlignedScrollTop({
    scrollTop: scroller.scrollTop,
    scrollHeight: scroller.scrollHeight,
    clientHeight: scroller.clientHeight,
    anchorBottom: anchorRect.bottom,
    scrollerBottom: scrollerRect.bottom,
  });
  return scroller;
}
