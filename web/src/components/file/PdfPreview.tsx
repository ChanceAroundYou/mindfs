import React, { useEffect, useRef, useState } from "react";
import type { PDFDocumentLoadingTask, PDFDocumentProxy, PDFPageProxy } from "pdfjs-dist";
import { useI18n } from "../../i18n/index";
import type { MessageKey, MessageParams } from "../../i18n/types";
import { visiblePageRange, type PdfPageLayout } from "../../services/documentPreview";
import { DocumentPreviewError, describeDocumentPreviewError } from "./DocumentPreviewError";

/** 往上找真正在滚动的祖先：谁的 overflow 是 auto/scroll 谁才是滚动容器。 */
function findScrollParent(element: HTMLElement): HTMLElement | null {
  let current: HTMLElement | null = element.parentElement;
  while (current) {
    const overflowY = window.getComputedStyle(current).overflowY;
    if (overflowY === "auto" || overflowY === "scroll" || overflowY === "overlay") {
      return current;
    }
    current = current.parentElement;
  }
  return null;
}

const PDF_PAGE_GAP_PX = 18;
const PDF_MAX_CSS_SCALE = 1.6;

/** 页面按容器宽度缩放，但不超过 PDF_MAX_CSS_SCALE（放大只会更糊）。 */
function cssScaleFor(pageWidth: number, availableWidth: number): number {
  if (!(pageWidth > 0)) return 1;
  return Math.min(PDF_MAX_CSS_SCALE, availableWidth / pageWidth);
}

/**
 * 只渲染视口附近的页，其余留占位。
 *
 * 之前是 for 循环把每一页都画成 canvas（DocumentViewer.tsx 旧 49-69 行），
 * canvas 全部同时挂在 DOM 上、永不释放：dpr=2 时单页 ~19MB、dpr=3 时 ~43MB，
 * 112 页的文档要 400MB~4GB backing store，分配失败就 reject 进 catch，
 * 整个预览变成一句「无法预览」。现在改成立占位 + 进出视口才画/释放。
 */
export function PdfPreview({ blob }: { blob: Blob }) {
  const { t } = useI18n();
  const containerRef = useRef<HTMLDivElement | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return undefined;
    const target: HTMLDivElement = container;
    let cancelled = false;
    let loadingTask: PDFDocumentLoadingTask | null = null;
    let pdfDocument: PDFDocumentProxy | null = null;
    let release: (() => void) | null = null;

    async function render() {
      try {
        const [pdfjs, workerModule] = await Promise.all([
          import("pdfjs-dist"),
          import("pdfjs-dist/build/pdf.worker.min.mjs?url"),
        ]);
        pdfjs.GlobalWorkerOptions.workerSrc = workerModule.default;
        const bytes = new Uint8Array(await blob.arrayBuffer());
        const task = pdfjs.getDocument({ data: bytes });
        loadingTask = task;
        const doc = await task.promise;
        if (cancelled) return;
        pdfDocument = doc;

        // 先量尺寸不画：112 页实测 370ms，比逐页渲染便宜两个数量级。
        // 量完才能算出每页该占多高，滚动条才不会随着 canvas 陆续出现而跳。
        const layouts: PdfPageLayout[] = [];
        for (let pageNumber = 1; pageNumber <= doc.numPages; pageNumber += 1) {
          const page = await doc.getPage(pageNumber);
          const viewport = page.getViewport({ scale: 1 });
          layouts.push({ width: viewport.width, height: viewport.height });
          page.cleanup();
        }
        if (cancelled) return;
        release = mountPageSlots(target, layouts, doc, t, () => cancelled, setError);
      } catch (err) {
        if (!cancelled) setError(describeDocumentPreviewError(err));
      }
    }

    void render();
    return () => {
      cancelled = true;
      release?.();
      release = null;
      void loadingTask?.destroy();
      pdfDocument?.destroy?.();
    };
  }, [blob, t]);

  if (error) return <DocumentPreviewError detail={error} />;
  return <div ref={containerRef} className="document-preview-pdf" />;
}

/**
 * 给每一页立一个占位块，只把视口附近的页画成 canvas。
 *
 * 返回一个清理函数。
 *
 * 为什么不全画：旧实现 for 循环把每一页都渲染成 canvas 且全部挂在 DOM 上、
 * 永不释放。dpr=2 时单页 backing store 约 19MB、dpr=3 时约 43MB，112 页的文档
 * 要 400MB~4GB，分配失败就 reject 进 catch，整个预览变成一句「无法预览」。
 */
function mountPageSlots(
  target: HTMLDivElement,
  layouts: readonly PdfPageLayout[],
  doc: PDFDocumentProxy,
  t: (key: MessageKey, params?: MessageParams) => string,
  isCancelled: () => boolean,
  onError: (detail: string) => void,
): () => void {
  target.replaceChildren();
  const scroller = findScrollParent(target);
  const slotWidth = (): number => Math.max(320, Math.min(1100, target.clientWidth - 32));

  const slots: HTMLDivElement[] = layouts.map((layout, index) => {
    const slot = window.document.createElement("div");
    slot.className = "document-preview-pdf-slot";
    slot.style.width = `${Math.min(slotWidth(), layout.width * PDF_MAX_CSS_SCALE)}px`;
    slot.style.height = `${Math.round(layout.height * cssScaleFor(layout.width, slotWidth()))}px`;
    slot.setAttribute("aria-label", t("fileViewer.pdfPage", { page: index + 1 }));
    target.appendChild(slot);
    return slot;
  });

  const mounted = new Map<number, HTMLCanvasElement>();
  let range = { start: -1, end: -1 };

  const drop = (index: number) => {
    const canvas = mounted.get(index);
    if (!canvas) return;
    mounted.delete(index);
    // 置 0 释放 backing store；slot 高度不变，滚动条不跳。
    canvas.width = 0;
    canvas.height = 0;
    canvas.remove();
  };

  const paint = async (index: number) => {
    if (isCancelled() || mounted.has(index) || index < 0 || index >= slots.length) return;
    const canvas = window.document.createElement("canvas");
    canvas.className = "document-preview-pdf-page";
    canvas.setAttribute("aria-label", t("fileViewer.pdfPage", { page: index + 1 }));
    let page: PDFPageProxy | null = null;
    try {
      page = await doc.getPage(index + 1);
      if (isCancelled() || mounted.has(index)) return;
      const baseViewport = page.getViewport({ scale: 1 });
      const pixelRatio = Math.min(window.devicePixelRatio || 1, 2);
      const renderViewport = page.getViewport({ scale: cssScaleFor(baseViewport.width, slotWidth()) * pixelRatio });
      canvas.width = Math.ceil(renderViewport.width);
      canvas.height = Math.ceil(renderViewport.height);
      canvas.style.width = `${Math.ceil(renderViewport.width / pixelRatio)}px`;
      canvas.style.height = `${Math.ceil(renderViewport.height / pixelRatio)}px`;
      const context = canvas.getContext("2d", { alpha: false });
      if (!context) throw new Error("canvas context unavailable");
      mounted.set(index, canvas);
      slots[index].replaceChildren(canvas);
      await page.render({ canvasContext: context, viewport: renderViewport }).promise;
    } catch (err) {
      // 页被滚出可视区而释放掉时，render 跟着 reject 是预期内的，不算错误。
      if (!isCancelled() && mounted.get(index) === canvas) {
        drop(index);
        onError(describeDocumentPreviewError(err));
      }
    } finally {
      page?.cleanup();
    }
  };

  const sync = () => {
    const next = visiblePageRange(
      layouts,
      scroller?.scrollTop ?? 0,
      scroller?.clientHeight || target.clientHeight || 800,
      PDF_PAGE_GAP_PX,
    );
    if (next.start === range.start && next.end === range.end) return;
    range = next;
    for (let index = 0; index < slots.length; index += 1) {
      if (index < next.start || index > next.end) drop(index);
    }
    for (let index = next.start; index <= next.end; index += 1) void paint(index);
  };

  sync();
  scroller?.addEventListener("scroll", sync, { passive: true });
  const resizeObserver = new ResizeObserver(sync);
  resizeObserver.observe(target);
  if (scroller) resizeObserver.observe(scroller);

  return () => {
    resizeObserver.disconnect();
    scroller?.removeEventListener("scroll", sync);
    for (const index of Array.from(mounted.keys())) drop(index);
    target.replaceChildren();
  };
}
