import React, { useEffect, useRef, useState } from "react";
import type { PDFDocumentLoadingTask, PDFDocumentProxy, PDFPageProxy } from "pdfjs-dist";
import { useI18n } from "../../i18n/index";
import type { MessageKey, MessageParams } from "../../i18n/types";
import type { PdfPageLayout } from "../../shared/documentPreviewTypes";
import { DocumentPreviewError } from "./DocumentPreviewError";
import { describeDocumentPreviewError } from "../../shared/documentPreview";

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

function cssScaleFor(pageWidth: number, availableWidth: number): number {
  if (!(pageWidth > 0)) return 1;
  return Math.min(PDF_MAX_CSS_SCALE, availableWidth / pageWidth);
}

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

type MountedEntry = {
  canvas: HTMLCanvasElement;
  renderTask: { cancel: () => void } | null;
};

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

  const sizeSlot = (slot: HTMLDivElement, layout: PdfPageLayout): void => {
    const available = slotWidth();
    slot.style.width = `${Math.min(available, layout.width * PDF_MAX_CSS_SCALE)}px`;
    slot.style.height = `${Math.round(layout.height * cssScaleFor(layout.width, available))}px`;
  };

  const slots: HTMLDivElement[] = layouts.map((layout, index) => {
    const slot = window.document.createElement("div");
    slot.className = "document-preview-pdf-slot";
    sizeSlot(slot, layout);
    slot.setAttribute("aria-label", t("fileViewer.pdfPage", { page: index + 1 }));
    target.appendChild(slot);
    return slot;
  });

  const mounted = new Map<number, MountedEntry>();
  const inFlight = new Map<number, Promise<void>>();
  const failed = new Set<number>();

  const drop = (index: number) => {
    const entry = mounted.get(index);
    if (!entry) return;
    try {
      entry.renderTask?.cancel();
    } catch {
      // ignore
    }
    mounted.delete(index);
    entry.canvas.width = 0;
    entry.canvas.height = 0;
    entry.canvas.remove();
  };

  const paint = async (index: number): Promise<void> => {
    if (isCancelled() || index < 0 || index >= slots.length) return;
    const existing = inFlight.get(index);
    if (existing) return existing;

    const promise = (async () => {
      if (isCancelled() || mounted.has(index)) return;
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
        mounted.set(index, { canvas, renderTask: null });
        slots[index].replaceChildren(canvas);
        const task = page.render({ canvasContext: context, viewport: renderViewport });
        const entry = mounted.get(index);
        if (entry) entry.renderTask = task;
        await task.promise;
        failed.delete(index);
      } catch (err) {
        if (isCancelled()) return;
        if (mounted.get(index)?.canvas === canvas) {
          drop(index);
        }
        const message = describeDocumentPreviewError(err);
        if (!/cancel/i.test(message)) {
          failed.add(index);
          onError(message);
        }
      } finally {
        page?.cleanup();
      }
    })();

    inFlight.set(index, promise);
    try {
      await promise;
    } finally {
      inFlight.delete(index);
    }
  };

  // IntersectionObserver 比手算 scrollTop 可靠：覆盖任何滚动容器与窗口滚动。
  const observer = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        const index = slots.indexOf(entry.target as HTMLDivElement);
        if (index < 0) continue;
        if (entry.isIntersecting) {
          if (!mounted.has(index) || failed.has(index)) {
            failed.delete(index);
            void paint(index);
          }
        } else {
          drop(index);
        }
      }
    },
    { root: scroller, rootMargin: "1200px", threshold: 0 },
  );
  slots.forEach((slot) => observer.observe(slot));

  // 尺寸变化要重渲染：页宽按 target.clientWidth 算，IntersectionObserver 只看可见性变化，
  // 窗口/抽屉改宽时可见性没变，页会停在旧尺寸（滚出去再滚回来才重画）。
  // lastWidth 守自激：改 slot 尺寸会改变 target 高度，本身又会再触发一次 ResizeObserver。
  let lastWidth = slotWidth();
  const sync = () => {
    const width = slotWidth();
    if (width === lastWidth) return;
    lastWidth = width;
    for (let index = 0; index < slots.length; index += 1) sizeSlot(slots[index]!, layouts[index]!);
    for (const index of Array.from(mounted.keys())) {
      drop(index); // paint 见 mounted 已存在会直接返回，必须先释放
      void paint(index);
    }
  };
  const resizeObserver = new ResizeObserver(sync);
  resizeObserver.observe(target);

  // 首屏兜底（IO 首次回调可能异步）
  slots.forEach((_, index) => {
    const rect = slots[index].getBoundingClientRect();
    const rootRect = scroller
      ? scroller.getBoundingClientRect()
      : { top: 0, bottom: window.innerHeight };
    if (rect.bottom >= rootRect.top - 1200 && rect.top <= rootRect.bottom + 1200) {
      void paint(index);
    }
  });

  return () => {
    observer.disconnect();
    resizeObserver.disconnect();
    for (const index of Array.from(mounted.keys())) drop(index);
    target.replaceChildren();
  };
}
