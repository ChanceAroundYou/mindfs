// 节点浮动卡片（inspector）的定位与锚点查询。
//
// 为什么要单独一层：
// 1. 卡片必须在被选节点旁边，而画布会被缩放/平移 —— 位置既要用纯函数算
//    （右侧放不下就翻到左侧、顶部放不下就往上收），也要能重新量到节点当前的
//    屏幕矩形。
// 2. 卡片必须 portal 到 document.body。祖先 `<main class="mindfs-font-scale-region">`
//    带 `contain: paint`，它会给后代的 `position: fixed` 建立包含块，于是
//    「固定定位」实际相对 main 而不是视口，整张卡横向偏移一个侧边栏宽度
//    （实测 +260px，y 还会被夹到底部）—— 用户看到的正是「卡片出现在原地，
//    不在节点附近」。portal 出去才能让 fixed 真正贴视口。

/** 卡片最大宽度，与 WorkflowNodeInspector 的 maxWidth 保持一致。 */
export const INSPECTOR_MAX_WIDTH = 400;
/** 卡片高度只能估：内容随节点类型变化，测量要等渲染完，点击时还没有。 */
export const INSPECTOR_ESTIMATED_HEIGHT = 500;
const VIEWPORT_MARGIN = 10;

export type ScreenRect = { left: number; top: number; right: number };
export type ViewportSize = { width: number; height: number };

/** 贴着节点右边缘放；右侧不够翻到左侧；两侧都不够就贴着视口右边。 */
export function placeInspectorNear(rect: ScreenRect, viewport: ViewportSize): { x: number; y: number } {
  let x = rect.right + VIEWPORT_MARGIN;
  let y = rect.top;
  if (x + INSPECTOR_MAX_WIDTH > viewport.width) {
    x = rect.left - INSPECTOR_MAX_WIDTH - VIEWPORT_MARGIN;
    if (x < VIEWPORT_MARGIN) {
      x = Math.max(VIEWPORT_MARGIN, viewport.width - INSPECTOR_MAX_WIDTH - VIEWPORT_MARGIN);
    }
  }
  if (y + INSPECTOR_ESTIMATED_HEIGHT > viewport.height) {
    y = Math.max(VIEWPORT_MARGIN, viewport.height - INSPECTOR_ESTIMATED_HEIGHT - VIEWPORT_MARGIN);
  }
  return { x, y };
}

/**
 * 按节点 id 量它在屏幕上的矩形。必须量整个 `.react-flow__node` 框：点击落点
 * 通常是内部图标或文字。
 */
export function nodeScreenRect(
  doc: { querySelector(selector: string): { getBoundingClientRect(): ScreenRect } | null },
  nodeId: string,
): ScreenRect | null {
  if (!nodeId) return null;
  // id 会写进属性选择器，先把引号/反斜杠转义掉，免得存量草稿里的怪 id 直接抛错。
  const safe = nodeId.replace(/["\\]/g, "\\$&");
  const el = doc.querySelector(`.react-flow__node[data-id="${safe}"]`);
  return el ? el.getBoundingClientRect() : null;
}
