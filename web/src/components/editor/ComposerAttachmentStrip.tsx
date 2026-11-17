import type React from "react";
import { useI18n } from "../../i18n";
import { CompactUploadProgress } from "../file/CompactUploadProgress";
import type { ComposerAttachment, UploadProgress } from "../../shared/uploadTypes";

export type { ComposerAttachment } from "../../shared/uploadTypes";

export type ComposerAttachmentStripProps = {
  attachments: ComposerAttachment[];
  uploadProgress?: UploadProgress | null;
  disabled?: boolean;
  onRemove: (id: string) => void;
  onCancelUpload?: () => void;
};

/**
 * 输入框下方的待发送附件条。
 *
 * 新建任务面板与任务对话输入框共用这一份：图片给缩略图、其它文件给胶囊，
 * 上传中显示进度并提供取消。两处各写一遍时，圆角、尺寸和移除按钮已经漂移过，
 * 所以抽成同一个 presenter。
 */
export function ComposerAttachmentStrip({
  attachments,
  uploadProgress,
  disabled = false,
  onRemove,
  onCancelUpload,
}: ComposerAttachmentStripProps) {
  const { t } = useI18n();
  if (attachments.length === 0 && !uploadProgress) return null;

  return (
    <div
      data-testid="composer-attachments"
      style={{ marginTop: 10, display: "flex", flexWrap: "wrap", gap: 8 }}
    >
      {attachments.map((attachment) =>
        attachment.isImage && attachment.previewUrl ? (
          <div
            key={attachment.id}
            style={{
              width: "54px",
              height: "54px",
              borderRadius: "8px",
              border: "1px solid var(--border-color)",
              background: "rgba(100, 116, 139, 0.10)",
              position: "relative",
              overflow: "hidden",
            }}
          >
            <img
              src={attachment.previewUrl}
              alt={attachment.file.name}
              style={{ width: "100%", height: "100%", objectFit: "cover", display: "block" }}
            />
            <button
              type="button"
              onClick={() => onRemove(attachment.id)}
              disabled={disabled}
              aria-label={t("task.removeAttachment", { name: attachment.file.name })}
              style={removeImageStyle(disabled)}
            >
              ×
            </button>
          </div>
        ) : (
          <span
            key={attachment.id}
            style={{
              maxWidth: "100%",
              border: "1px solid var(--border-color)",
              borderRadius: "999px",
              background: "rgba(100, 116, 139, 0.10)",
              color: "var(--text-color)",
              display: "inline-flex",
              alignItems: "center",
              gap: "6px",
              padding: "4px 7px",
              fontSize: "12px",
            }}
          >
            <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
              {attachment.file.name}
            </span>
            <button
              type="button"
              onClick={() => onRemove(attachment.id)}
              disabled={disabled}
              aria-label={t("task.removeAttachment", { name: attachment.file.name })}
              style={removeChipStyle(disabled)}
            >
              ×
            </button>
          </span>
        ),
      )}
      <CompactUploadProgress
        progress={uploadProgress ?? null}
        label={t("upload.attachmentsProgress")}
        statusLabel={t("upload.inProgress")}
        cancelLabel={t("upload.cancel")}
        onCancel={onCancelUpload}
      />
    </div>
  );
}

function removeImageStyle(disabled: boolean): React.CSSProperties {
  return {
    position: "absolute",
    top: "2px",
    right: "2px",
    width: "18px",
    height: "18px",
    border: "none",
    borderRadius: "999px",
    background: "rgba(15, 23, 42, 0.72)",
    color: "#fff",
    cursor: disabled ? "not-allowed" : "pointer",
    padding: 0,
    lineHeight: "18px",
  };
}

function removeChipStyle(disabled: boolean): React.CSSProperties {
  return {
    border: "none",
    background: "transparent",
    color: "var(--text-secondary)",
    cursor: disabled ? "not-allowed" : "pointer",
    padding: 0,
  };
}