import React, { memo, useEffect, useRef, useState } from "react";
import { DocumentPreviewError as ErrorMessage } from "./DocumentPreviewError";
import { describeDocumentPreviewError as describeError } from "../../shared/documentPreview";
import { useDocumentPreviewBlob } from "./useDocumentPreviewBlob";
import { PdfPreview } from "./PdfPreview";
import { useI18n } from "../../i18n/index";
import type { DocumentPreviewKind } from "../../shared/documentPreviewTypes";

type DocumentViewerProps = {
  path: string;
  root?: string;
  kind: DocumentPreviewKind;
};

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
  const state = useDocumentPreviewBlob(root, path);

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
