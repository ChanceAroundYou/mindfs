export type DocumentPreviewKind = "pdf" | "word" | "excel" | "powerpoint";

const DOCUMENT_EXTENSIONS: Record<DocumentPreviewKind, ReadonlySet<string>> = {
  pdf: new Set([".pdf"]),
  word: new Set([".docx"]),
  excel: new Set([".xlsx"]),
  powerpoint: new Set([".pptx"]),
};

export function getDocumentPreviewKind(ext: string, mime = ""): DocumentPreviewKind | null {
  const normalizedExt = ext.trim().toLowerCase();
  for (const [kind, extensions] of Object.entries(DOCUMENT_EXTENSIONS) as Array<[DocumentPreviewKind, ReadonlySet<string>]>) {
    if (extensions.has(normalizedExt)) return kind;
  }
  const normalizedMime = mime.toLowerCase();
  if (normalizedMime === "application/pdf") return "pdf";
  if (normalizedMime.includes("wordprocessingml")) return "word";
  if (normalizedMime.includes("spreadsheetml")) return "excel";
  if (normalizedMime.includes("presentationml")) return "powerpoint";
  return null;
}

/** 一页 PDF 在给定缩放下的 CSS 像素尺寸。 */
export type PdfPageLayout = {
  /** 1pt 下的页面尺寸（pdf.js getViewport({scale:1}) 的结果） */
  width: number;
  height: number;
};

/** 视口上下各多渲染一屏，避免快速滚动时白屏。 */
export const PDF_RENDER_MARGIN_PX = 1200;

/**
 * 由页面累积高度 + 视口位置，算出要渲染的页号区间（0-based，闭区间）。
 * 纯函数：不碰 DOM，测试直接喂 scrollTop / viewportH。
 *
 * 抽取出来是因为「渲染哪些页」是整段逻辑里唯一有分支的部分，
 * 也就是唯一值得留断言的地方。
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
    // 视口落在所有页之外：夹到最接近的一页，start <= end 恒成立
    const lastPageTop = offset - gap - Math.max(1, layouts[lastIndex].height);
    const closest = top > lastPageTop ? lastIndex : 0;
    return { start: closest, end: closest };
  }
  return { start, end };
}
