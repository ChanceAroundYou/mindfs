export type DocumentPreviewKind = "pdf" | "word" | "excel" | "powerpoint";

/** A PDF page's CSS pixel dimensions at scale 1. */
export type PdfPageLayout = {
  width: number;
  height: number;
};
