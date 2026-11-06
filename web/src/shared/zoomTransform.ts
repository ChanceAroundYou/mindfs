/**
 * 缩放/拖动的纯数学。
 *
 * 起初只服务于图表预览（`diagramZoom.ts` 是它的旧名门面），图片预览要做同样的
 * 「按住锚点缩放、拖动平移」之后上移到这里 —— 两处共用同一份夹取与坐标换算，
 * 免得一边能放到 16 倍、另一边悄悄只到 4 倍。
 *
 * 坐标约定：`from` / `to` 是**相对变换原点**的点（原点 = 内容中心）。
 */
export type ZoomPoint = { x: number; y: number };
export type ZoomTransform = { x: number; y: number; scale: number };

export const ZOOM_MIN_SCALE = 0.25;
export const ZOOM_MAX_SCALE = 16;

/** 以 `from` 为锚点缩放 `factor` 倍；`to` 给出时把 `from` 拖到 `to`（平移用 factor=1）。 */
export function zoomAt(transform: ZoomTransform, factor: number, from: ZoomPoint, to = from): ZoomTransform {
  const scale = Math.min(ZOOM_MAX_SCALE, Math.max(ZOOM_MIN_SCALE, transform.scale * factor));
  const ratio = scale / transform.scale;
  return { scale, x: to.x - (from.x - transform.x) * ratio, y: to.y - (from.y - transform.y) * ratio };
}

/** 手势中点与两指距离；单指时 distance=0（调用方据此只做平移）。 */
export function gestureMetrics(points: ZoomPoint[]) {
  const [a, b = a] = points;
  return { center: { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 }, distance: Math.hypot(b.x - a.x, b.y - a.y) };
}
