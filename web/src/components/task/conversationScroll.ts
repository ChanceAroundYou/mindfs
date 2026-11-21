// 打开任务详情时把面板滚到真正的底部。
//
// 任务详情不是独立滚动区：`.mindfs-panel-body` 是唯一滚动容器（对话、执行详情
// 胶囊、完成证据都在里面）。**目标必须是容器自己的底部，不能只对齐对话末尾**：
// 桌面端对话下方还有「执行详情 / 监管记录」两个胶囊和收尾区，锚点对齐会把它们
// 留在视口之外（实测打开后胶囊顶边 855 > 可视底 845，「回到底部」也就不到底）。
//
// 只负责几何计算与一次对齐，不关心何时调用（视图层在首屏数据到达后调用）。

export type PanelBottomInput = {
  scrollHeight: number;
  clientHeight: number;
};

/**
 * 面板真正底部对应的 scrollTop（已夹到 ≥0）。
 *
 * 纯函数：内容还没铺满（最大滚动为 0）时返回 0。目标与「回到底部」按钮的收敛
 * 终点是同一个值（`scrollHeight - clientHeight`），所以打开时落到哪里、点按钮
 * 之后停在哪里不会出现两种口径。
 */
export function panelBottomScrollTop(input: PanelBottomInput): number {
  return Math.max(0, input.scrollHeight - input.clientHeight);
}

/**
 * 从锚点向上找最近的滚动容器（overflowY auto/scroll），**不看内容高度**。
 *
 * 「回到底部」按钮要在内容还没撑满时就把滚动监听挂上：这时容器不可滚、按钮该隐藏，
 * 但之后来一条长回复就会变成可滚，监听必须已经在。
 */
export function findScrollContainer(start: HTMLElement): HTMLElement | null {
  let node: HTMLElement | null = start.parentElement;
  while (node) {
    const style = window.getComputedStyle(node);
    if (style.overflowY === "auto" || style.overflowY === "scroll") return node;
    node = node.parentElement;
  }
  return null;
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
 * 把整个面板（对话 + 下方的胶囊与收尾区）滚到真正的底部；返回该容器
 * （找不到时为 null）。对齐后再次调用是幂等的，视图层可以在布局稳定的头几帧里
 * 重复调用（异步展开的执行详情会把 scrollHeight 继续撑大）。
 */
export function alignPanelToBottom(anchor: HTMLElement): HTMLElement | null {
  const scroller = findScrollableAncestor(anchor);
  if (!scroller) return null;
  scroller.scrollTop = panelBottomScrollTop({
    scrollHeight: scroller.scrollHeight,
    clientHeight: scroller.clientHeight,
  });
  return scroller;
}
