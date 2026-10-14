import { useI18n } from "../../i18n/index";
export { describeDocumentPreviewError } from "../../shared/documentPreview";

export function DocumentPreviewError({ detail }: { detail?: string }) {
  const { t } = useI18n();
  return (
    <div className="document-preview-message document-preview-error">
      <div>{t("fileViewer.previewFailed")}</div>
      {detail ? <div className="document-preview-error-detail">{t("fileViewer.previewFailedReason", { reason: detail })}</div> : null}
    </div>
  );
}
