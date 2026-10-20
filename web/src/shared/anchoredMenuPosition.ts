/**
 * 锚定弹层的定位计算（纯函数，不碰 DOM）。
 *
 * 菜单一律 portal 到 body 并用 position: fixed 定位：挂载它的容器多半是
 * overflow:auto（任务面板、文件列表），内联/绝对定位的菜单会被裁掉；而带
 * transform 的祖先（列表行有 translateZ(0)）还会给 fixed 造出包含块。
 *
 * 定位规则与 Select 菜单一直以来的行为一致：
 *   - 水平：默认右边缘对齐锚点右边缘，再夹进视口；
 *   - 垂直：默认放下方，下方放不下就翻到上方。
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
};

export function computeAnchoredMenuPosition({
  anchor,
  menu,
  viewport,
  align = "right",
  margin = 8,
}: {
  anchor: AnchoredMenuAnchor;
  /** 菜单自身尺寸。 */
  menu: { width: number; height: number };
  viewport: AnchoredMenuViewport;
  /** right = 菜单右边缘对齐锚点右边缘（默认）；left = 左边缘对齐锚点左边缘。 */
  align?: "left" | "right";
  margin?: number;
}): AnchoredMenuPosition {
  const aligned = align === "left" ? anchor.left : anchor.right - menu.width;
  const left = Math.max(
    viewport.left + margin,
    Math.min(aligned, viewport.left + viewport.width - menu.width - margin),
  );
  const below = anchor.bottom + margin;
  const above = anchor.top - menu.height - margin;
  const fitsBelow = below + menu.height <= viewport.top + viewport.height - margin;
  const top = fitsBelow ? below : Math.max(viewport.top + margin, above);
  return { top, left };
}
