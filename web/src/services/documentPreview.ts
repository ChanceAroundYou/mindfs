import type { DocumentPreviewKind, PdfPageLayout } from "../shared/documentPreviewTypes";
export type { DocumentPreviewKind, PdfPageLayout } from "../shared/documentPreviewTypes";
export { PDF_RENDER_MARGIN_PX, visiblePageRange } from "../shared/documentPreview";

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
