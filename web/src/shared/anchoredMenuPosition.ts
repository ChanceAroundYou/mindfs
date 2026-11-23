/**
 * 锚定弹层的定位计算（纯函数，不碰 DOM）。
 *
 * 菜单一律 portal 到 body 并用 position: fixed 定位：挂载它的容器多半是
 * overflow:auto（任务面板、文件列表），内联/绝对定位的菜单会被裁掉；而带
 * transform 的祖先（列表行有 translateZ(0)）还会给 fixed 造出包含块。
 *
 * 定位规则与 Select 菜单一直以来的行为一致：
 *   - 水平：默认右边缘对齐锚点右边缘，再夹进视口；
 *   - 垂直：默认放下方，下方放不下就翻到上方（prefer 可改成优先上方）。
 *
 * 两侧都放不下时不溢出屏幕，而是落在空间更大的一侧，并用返回的 maxHeight
 * 让菜单内部滚动 —— 输入框贴着面板底部时，软键盘压矮可视视口就会走到这一支。
 */

/** 锚点矩形（只需要 DOMRect 的四个边）。 */
export type AnchoredMenuAnchor = {
  top: number;
  bottom: number;
  left: number;
  right: number;
};

/** 可视区域（visualViewport 的 offset/宽高）。 */
export type AnchoredMenuViewport = {
  top: number;
  left: number;
  width: number;
  height: number;
};

export type AnchoredMenuPosition = {
  top: number;
  left: number;
  /** 该侧可用高度（像素）。调用方应写成 maxHeight，超出时菜单内部滚动。 */
  maxHeight: number;
  /** 视口内可用的最大宽度（已扣掉左右 margin）。 */
  maxWidth: number;
};

export function computeAnchoredMenuPosition({
  anchor,
  menu,
  viewport,
  align = "right",
  margin = 8,
  prefer = "below",
}: {
  anchor: AnchoredMenuAnchor;
  /** 菜单自身尺寸。 */
  menu: { width: number; height: number };
  viewport: AnchoredMenuViewport;
  /** right = 菜单右边缘对齐锚点右边缘（默认）；left = 左边缘对齐锚点左边缘。 */
  align?: "left" | "right";
  margin?: number;
  /** 上下都放得下时优先哪一侧；默认下方（Select 等既有消费者依赖这一默认）。 */
  prefer?: "below" | "above";
}): AnchoredMenuPosition {
  const aligned = align === "left" ? anchor.left : anchor.right - menu.width;
  const left = Math.max(
    viewport.left + margin,
    Math.min(aligned, viewport.left + viewport.width - menu.width - margin),
  );

  // 两侧各扣两个 margin：菜单与锚点之间的间距 + 菜单与视口边缘之间的间距。
  const spaceAbove = Math.max(0, anchor.top - viewport.top - margin * 2);
  const spaceBelow = Math.max(0, viewport.top + viewport.height - anchor.bottom - margin * 2);

  let placeAbove: boolean;
  if (menu.height <= spaceAbove && menu.height <= spaceBelow) {
    placeAbove = prefer === "above";
  } else if (menu.height <= spaceAbove) {
    placeAbove = true;
  } else if (menu.height <= spaceBelow) {
    placeAbove = false;
  } else {
    // 两侧都放不下：取空间更大的一侧，同高时按 prefer。
    placeAbove = spaceAbove > spaceBelow || (spaceAbove === spaceBelow && prefer === "above");
  }

  const available = placeAbove ? spaceAbove : spaceBelow;
  // 用夹后的高度定位：maxHeight 只依赖锚点与视口，不会随菜单自身收缩而振荡。
  const height = Math.min(menu.height, available);
  const top = Math.max(
    viewport.top + margin,
    Math.min(
      placeAbove ? anchor.top - height - margin : anchor.bottom + margin,
      viewport.top + viewport.height - margin - height,
    ),
  );

  return {
    top,
    left,
    maxHeight: Math.floor(available),
    maxWidth: Math.max(0, viewport.width - margin * 2),
  };
}