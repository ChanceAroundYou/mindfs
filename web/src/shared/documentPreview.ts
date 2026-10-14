import type { PdfPageLayout } from "./documentPreviewTypes";

/** 把异常压成一行人话，避免把解析/内存异常压成无法定位的空错误。 */
export function describeDocumentPreviewError(err: unknown): string {
  if (err instanceof Error) {
    const message = err.message.trim();
    if (message) return message.length > 200 ? `${message.slice(0, 200)}…` : message;
    return err.name || "Error";
  }
  const text = String(err ?? "").trim();
  if (!text) return "unknown error";
  return text.length > 200 ? `${text.slice(0, 200)}…` : text;
}

/** 视口上下各多渲染一屏，避免快速滚动时白屏。 */
export const PDF_RENDER_MARGIN_PX = 1200;

/**
 * 由页面累积高度 + 视口位置，算出要渲染的页号区间（0-based，闭区间）。
 * 纯函数：不碰 DOM，测试直接喂 scrollTop / viewportH。
 *
 * start/end 永远指向真实存在的页——滚动位置非法（内容还没布局完、
 * 容器已卸载）时夹到最近的页，不会返回空区间，否则首屏什么都不画。
 */
export function visiblePageRange(
  layouts: readonly PdfPageLayout[],
  scrollTop: number,
  viewportH: number,
  gap: number,
  marginPx: number = PDF_RENDER_MARGIN_PX,
): { start: number; end: number } {
  const lastIndex = layouts.length - 1;
  if (lastIndex < 0) {
    return { start: 0, end: -1 };
  }
  const top = scrollTop - marginPx;
  const bottom = scrollTop + Math.max(0, viewportH) + marginPx;
  let start = layouts.length;
  let end = -1;
  let offset = 0;
  for (let index = 0; index <= lastIndex; index += 1) {
    const pageTop = offset;
    const pageBottom = pageTop + Math.max(1, layouts[index].height);
    offset = pageBottom + gap;
    if (pageBottom >= top && pageTop <= bottom) {
      if (index < start) start = index;
      if (index > end) end = index;
    }
  }
  if (end < start) {
    const lastPageTop = offset - gap - Math.max(1, layouts[lastIndex].height);
    const closest = top > lastPageTop ? lastIndex : 0;
    return { start: closest, end: closest };
  }
  return { start, end };
}
