import { useI18n } from "../../i18n/index";

export function DocumentPreviewError({ detail }: { detail?: string }) {
  const { t } = useI18n();
  return (
    <div className="document-preview-message document-preview-error">
      <div>{t("fileViewer.previewFailed")}</div>
      {detail ? <div className="document-preview-error-detail">{t("fileViewer.previewFailedReason", { reason: detail })}</div> : null}
    </div>
  );
}

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
