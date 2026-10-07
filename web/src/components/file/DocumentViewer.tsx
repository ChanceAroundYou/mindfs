import React, { memo, useEffect, useRef, useState } from "react";
import type { PDFDocumentLoadingTask, PDFDocumentProxy, PDFPageProxy } from "pdfjs-dist";
import { fetchProofProtectedBlob } from "../../services/file";
import { useI18n } from "../../i18n/index";
import type { MessageKey, MessageParams } from "../../i18n/types";
import { visiblePageRange, type DocumentPreviewKind, type PdfPageLayout } from "../../services/documentPreview";

type DocumentViewerProps = {
  path: string;
  root?: string;
  kind: DocumentPreviewKind;
};

type PreviewState =
  | { status: "loading" }
  | { status: "ready"; blob: Blob }
  | { status: "error"; detail?: string };

function ErrorMessage({ detail }: { detail?: string }) {
  const { t } = useI18n();
  return (
    <div className="document-preview-message document-preview-error">
      <div>{t("fileViewer.previewFailed")}</div>
      {detail ? <div className="document-preview-error-detail">{t("fileViewer.previewFailedReason", { reason: detail })}</div> : null}
    </div>
  );
}

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
 * 把异常压成一行人话。
 * 之前所有分支都是 `catch { setError(true) }`，用户只看到「无法预览」，
 * 分不清是取文件失败、格式损坏还是内存分配失败——白排查。
 */
function describeError(err: unknown): string {
  if (err instanceof Error) {
    const message = err.message.trim();
    if (message) return message.length > 200 ? `${message.slice(0, 200)}…` : message;
    return err.name || "Error";
  }
  const text = String(err ?? "").trim();
  if (!text) return "unknown error";
  return text.length > 200 ? `${text.slice(0, 200)}…` : text;
}

/**
 * 只渲染视口附近的页，其余留占位。
 *
 * 之前是 for 循环把每一页都画成 canvas（DocumentViewer.tsx 旧 49-69 行），
 * canvas 全部同时挂在 DOM 上、永不释放：dpr=2 时单页 ~19MB、dpr=3 时 ~43MB，
 * 112 页的文档要 400MB~4GB backing store，分配失败就 reject 进 catch，
 * 整个预览变成一句「无法预览」。现在改成立占位 + 进出视口才画/释放。
 */
function PdfPreview({ blob }: { blob: Blob }) {
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
        if (!cancelled) setError(describeError(err));
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

  if (error) return <ErrorMessage detail={error} />;
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
        onError(describeError(err));
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

function WordPreview({ blob }: { blob: Blob }) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return undefined;
    let cancelled = false;
    let rendered = false;
    container.replaceChildren();

    const fitPages = () => {
      if (!rendered || cancelled) return;
      const wrapper = container.querySelector<HTMLElement>(".mindfs-docx-wrapper");
      if (!wrapper) return;
      const wrapperStyle = window.getComputedStyle(wrapper);
      const horizontalPadding = Number.parseFloat(wrapperStyle.paddingLeft || "0")
        + Number.parseFloat(wrapperStyle.paddingRight || "0");
      const availableWidth = Math.max(1, wrapper.clientWidth - horizontalPadding);
      wrapper.querySelectorAll<HTMLElement>("section.mindfs-docx").forEach((page) => {
        if (page.dataset.mindfsOriginalPaddingLeft === undefined) {
          page.dataset.mindfsOriginalPaddingLeft = page.style.paddingLeft;
          page.dataset.mindfsOriginalPaddingRight = page.style.paddingRight;
        }
        page.style.setProperty("padding-left", page.dataset.mindfsOriginalPaddingLeft ?? "");
        page.style.setProperty("padding-right", page.dataset.mindfsOriginalPaddingRight ?? "");
        page.style.removeProperty("transform");
        const intrinsicPageWidth = page.offsetWidth;
        if (intrinsicPageWidth > 0) {
          const pageStyle = window.getComputedStyle(page);
          const previewMargin = Math.max(32, Math.min(64, intrinsicPageWidth * 0.06));
          if (Number.parseFloat(pageStyle.paddingLeft || "0") > previewMargin) {
            page.style.paddingLeft = `${previewMargin}px`;
          }
          if (Number.parseFloat(pageStyle.paddingRight || "0") > previewMargin) {
            page.style.paddingRight = `${previewMargin}px`;
          }
        }

        page.querySelectorAll<HTMLElement>("article > table").forEach((table) => {
          if (table.dataset.mindfsOriginalWidth === undefined) {
            table.dataset.mindfsOriginalWidth = table.style.width;
            table.dataset.mindfsOriginalMaxWidth = table.style.maxWidth;
            table.dataset.mindfsOriginalTableLayout = table.style.tableLayout;
          }
          table.style.setProperty("width", table.dataset.mindfsOriginalWidth ?? "");
          table.style.setProperty("max-width", table.dataset.mindfsOriginalMaxWidth ?? "");
          table.style.setProperty("table-layout", table.dataset.mindfsOriginalTableLayout ?? "");
          const columns = Array.from(table.querySelectorAll<HTMLElement>("col"));
          columns.forEach((column) => {
            if (column.dataset.mindfsOriginalWidth === undefined) {
              column.dataset.mindfsOriginalWidth = column.style.width;
            }
            column.style.setProperty("width", column.dataset.mindfsOriginalWidth ?? "");
          });
          const article = table.parentElement;
          if (article && table.scrollWidth > article.clientWidth + 1) {
            const columnWidths = columns.map((column) => column.getBoundingClientRect().width);
            const totalColumnWidth = columnWidths.reduce((sum, width) => sum + width, 0);
            if (totalColumnWidth > 0) {
              columns.forEach((column, index) => {
                column.style.setProperty("width", `${columnWidths[index] / totalColumnWidth * 100}%`, "important");
              });
            }
            table.style.setProperty("width", "100%", "important");
            table.style.setProperty("max-width", "100%", "important");
            table.style.setProperty("table-layout", "fixed");
          }
        });

        let shell = page.parentElement;
        if (!shell?.classList.contains("mindfs-docx-page-shell")) {
          shell = window.document.createElement("div");
          shell.className = "mindfs-docx-page-shell";
          page.before(shell);
          shell.appendChild(page);
        }

        page.style.removeProperty("zoom");
        const pageWidth = page.offsetWidth;
        const pageHeight = Math.max(page.offsetHeight, page.scrollHeight);
        if (pageWidth <= 0 || pageHeight <= 0) return;
        const scale = Math.min(1.5, availableWidth / pageWidth);
        page.style.transform = `scale(${scale})`;
        page.style.transformOrigin = "top left";
        shell.style.width = `${pageWidth * scale}px`;
        shell.style.height = `${pageHeight * scale}px`;
      });
    };

    const resizeObserver = new ResizeObserver(fitPages);
    resizeObserver.observe(container);
    void import("docx-preview")
      .then(({ renderAsync }) => renderAsync(blob, container, undefined, {
        className: "mindfs-docx",
        inWrapper: true,
        breakPages: true,
        ignoreLastRenderedPageBreak: false,
        useBase64URL: true,
      }))
      .then(() => {
        rendered = true;
        fitPages();
      })
      .catch((err) => {
        if (!cancelled) setError(describeError(err));
      });
    return () => {
      cancelled = true;
      resizeObserver.disconnect();
      container.replaceChildren();
    };
  }, [blob]);

  if (error) return <ErrorMessage detail={error} />;
  return <div ref={containerRef} className="document-preview-word" />;
}

type ExcelSheet = {
  name: string;
  rows: string[][];
  columnWidths: number[];
};

function excelCellText(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (value instanceof Date) return value.toLocaleString();
  if (typeof value === "object") {
    const record = value as Record<string, unknown>;
    if (typeof record.text === "string") return record.text;
    if (record.result !== undefined) return excelCellText(record.result);
    if (Array.isArray(record.richText)) {
      return record.richText.map((part) => excelCellText(part)).join("");
    }
  }
  return String(value);
}

function ExcelPreview({ blob }: { blob: Blob }) {
  const { t } = useI18n();
  const [sheets, setSheets] = useState<ExcelSheet[]>([]);
  const [activeSheet, setActiveSheet] = useState(0);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    async function load() {
      try {
        const { default: readXlsxFile } = await import("read-excel-file/browser");
        const parsedSheets = await readXlsxFile(blob);
        if (cancelled) return;
        const nextSheets = parsedSheets.map(({ sheet, data }) => {
          const rows = data.slice(0, 5000).map((row) => row.slice(0, 200).map(excelCellText));
          const columnCount = Math.max(0, ...rows.map((row) => row.length));
          const columnWidths = Array.from({ length: columnCount }, (_, columnIndex) => {
            const longestValue = rows.slice(0, 200).reduce((longest, row) => Math.max(longest, (row[columnIndex] || "").length), 0);
            return Math.max(64, Math.min(360, 24 + longestValue * 8));
          });
          return { name: sheet, rows, columnWidths };
        });
        setSheets(nextSheets);
        setActiveSheet(0);
      } catch (err) {
        if (!cancelled) setError(describeError(err));
      }
    }
    void load();
    return () => { cancelled = true; };
  }, [blob]);

  if (error) return <ErrorMessage detail={error} />;
  if (sheets.length === 0) return <div className="document-preview-message">{t("fileViewer.previewLoading")}</div>;
  const sheet = sheets[activeSheet];

  return (
    <div className="document-preview-excel">
      <div className="document-preview-sheet-tabs" role="tablist">
        {sheets.map((item, index) => (
          <button key={`${item.name}-${index}`} type="button" role="tab" aria-selected={index === activeSheet} className={index === activeSheet ? "active" : ""} onClick={() => setActiveSheet(index)}>
            {item.name}
          </button>
        ))}
      </div>
      <div className="document-preview-sheet-grid">
        <table>
          <colgroup>
            <col style={{ width: 48 }} />
            {sheet.columnWidths.map((width, index) => <col key={index} style={{ width }} />)}
          </colgroup>
          <thead><tr><th />{sheet.columnWidths.map((_, index) => <th key={index}>{excelColumnName(index + 1)}</th>)}</tr></thead>
          <tbody>
            {sheet.rows.map((row, rowIndex) => (
              <tr key={rowIndex}>
                <th>{rowIndex + 1}</th>
                {sheet.columnWidths.map((_, columnIndex) => <td key={columnIndex}>{row[columnIndex] || ""}</td>)}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function excelColumnName(column: number): string {
  let value = column;
  let result = "";
  while (value > 0) {
    value -= 1;
    result = String.fromCharCode(65 + (value % 26)) + result;
    value = Math.floor(value / 26);
  }
  return result;
}

function PowerPointPreview({ blob }: { blob: Blob }) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return undefined;
    const target = container;
    let cancelled = false;
    let renderVersion = 0;
    let resizeTimer = 0;
    let destroyPreview: (() => void) | null = null;
    const bufferPromise = blob.arrayBuffer();
    const previewModulePromise = import("pptx-preview");

    async function render() {
      const width = target.clientWidth;
      const height = target.clientHeight;
      if (width < 120 || height < 120) return;
      const version = ++renderVersion;
      try {
        const [{ init }, buffer] = await Promise.all([previewModulePromise, bufferPromise]);
        if (cancelled || version !== renderVersion) return;
        destroyPreview?.();
        target.replaceChildren();
        const previewer = init(target, { width, height, mode: "list" });
        destroyPreview = () => previewer.destroy();
        await previewer.preview(buffer.slice(0));
      } catch (err) {
        if (!cancelled && version === renderVersion) setError(describeError(err));
      }
    }

    const resizeObserver = new ResizeObserver(() => {
      window.clearTimeout(resizeTimer);
      resizeTimer = window.setTimeout(() => { void render(); }, 100);
    });
    resizeObserver.observe(target);
    void render();
    return () => {
      cancelled = true;
      renderVersion += 1;
      window.clearTimeout(resizeTimer);
      resizeObserver.disconnect();
      destroyPreview?.();
      target.replaceChildren();
    };
  }, [blob]);

  if (error) return <ErrorMessage detail={error} />;
  return <div ref={containerRef} className="document-preview-powerpoint" />;
}

function DocumentViewerInner({ path, root, kind }: DocumentViewerProps) {
  const { t } = useI18n();
  const [state, setState] = useState<PreviewState>({ status: "loading" });

  useEffect(() => {
    let cancelled = false;
    setState({ status: "loading" });
    if (!root) {
      setState({ status: "error", detail: "no project" });
      return undefined;
    }
    void fetchProofProtectedBlob({ rootId: root, path })
      .then((blob) => {
        if (!cancelled) setState({ status: "ready", blob });
      })
      .catch((err) => {
        if (!cancelled) setState({ status: "error", detail: describeError(err) });
      });
    return () => { cancelled = true; };
  }, [path, root]);

  if (state.status === "loading") return <div className="document-preview-message">{t("fileViewer.previewLoading")}</div>;
  if (state.status === "error") return <ErrorMessage detail={state.detail} />;
  if (kind === "pdf") return <PdfPreview blob={state.blob} />;
  if (kind === "word") return <WordPreview blob={state.blob} />;
  if (kind === "excel") return <ExcelPreview blob={state.blob} />;
  return <PowerPointPreview blob={state.blob} />;
}

export const DocumentViewer = memo(DocumentViewerInner, (previous, next) => (
  previous.path === next.path && previous.root === next.root && previous.kind === next.kind
));
