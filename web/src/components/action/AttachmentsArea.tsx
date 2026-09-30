import React from "react";
import { CompactUploadProgress } from "../CompactUploadProgress";
import { useI18n } from "../../i18n";
import { appAccentHexToRgba, getSelectionPreview } from "./styleHelpers";
import { type AttachedFileContext } from "./types";
import { type UploadProgress } from "../../services/upload";

export type PendingAttachment = {
  id: string;
  file: File;
  previewUrl?: string;
  isImage: boolean;
};

export function AttachmentsArea({
  attachedFileContext,
  pendingAttachments,
  uploadProgress,
  isDark,
  isMobile,
  accentHex,
  onCancelUpload,
  onClearFileContext,
  onRemoveAttachment,
}: {
  attachedFileContext?: AttachedFileContext | null;
  pendingAttachments: PendingAttachment[];
  uploadProgress?: UploadProgress | null;
  isDark: boolean;
  isMobile: boolean;
  accentHex: string;
  onCancelUpload: () => void;
  onClearFileContext?: () => void;
  onRemoveAttachment: (id: string) => void;
}) {
  const { t } = useI18n();
  const rgba = (alpha: number) => appAccentHexToRgba(accentHex, alpha);

  return (
    <>
      {attachedFileContext ? (
        <div style={{ display: "flex", flexWrap: "wrap", gap: "6px", padding: isMobile ? "6px 4px 0" : "0 4px" }}>
          <span
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: "8px",
              minWidth: 0,
              maxWidth: "100%",
              padding: "4px 8px",
              borderRadius: "999px",
              background: isDark ? rgba(0.14) : rgba(0.08),
              color: "var(--text-primary)",
              fontSize: "12px",
            }}
          >
            <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", maxWidth: isMobile ? "96px" : "140px" }}>
              {attachedFileContext.fileName}
            </span>
            {typeof attachedFileContext.startLine === "number" && typeof attachedFileContext.endLine === "number" ? (
              <span style={{ color: "var(--text-secondary)", whiteSpace: "nowrap" }}>
                {attachedFileContext.startLine}-{attachedFileContext.endLine}
              </span>
            ) : attachedFileContext.text ? (
              <span style={{ color: "var(--text-secondary)", whiteSpace: "nowrap" }}>
                {getSelectionPreview(attachedFileContext.text)}
              </span>
            ) : null}
            <button
              type="button"
              onClick={onClearFileContext}
              onMouseDown={(event) => event.preventDefault()}
              onTouchStart={(event) => event.preventDefault()}
              style={{
                border: "none",
                background: "transparent",
                color: "var(--text-secondary)",
                cursor: "pointer",
                padding: 0,
                lineHeight: 1,
                fontSize: "14px",
              }}
              aria-label={t("action.removeFileContext", { name: attachedFileContext.fileName })}
              title={t("action.removeItem", { name: attachedFileContext.fileName })}
            >
              ×
            </button>
          </span>
        </div>
      ) : null}
      {pendingAttachments.length > 0 ? (
        <div style={{ display: "flex", flexDirection: "column", gap: "8px", padding: isMobile ? "6px 4px 0" : "0 4px" }}>
          {pendingAttachments.some((attachment) => attachment.isImage) ? (
            <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(72px, 1fr))", gap: "8px" }}>
              {pendingAttachments
                .filter((attachment) => attachment.isImage && attachment.previewUrl)
                .map((attachment) => (
                  <div
                    key={attachment.id}
                    style={{
                      position: "relative",
                      borderRadius: "12px",
                      overflow: "hidden",
                      background: isDark ? "rgba(15,23,42,0.55)" : "rgba(15,23,42,0.06)",
                      aspectRatio: "1 / 1",
                    }}
                  >
                    <img
                      src={attachment.previewUrl}
                      alt={attachment.file.name}
                      style={{ display: "block", width: "100%", height: "100%", objectFit: "cover" }}
                    />
                    <button
                      type="button"
                      onClick={() => onRemoveAttachment(attachment.id)}
                      style={{
                        position: "absolute",
                        top: "6px",
                        right: "6px",
                        width: "22px",
                        height: "22px",
                        borderRadius: "999px",
                        border: "none",
                        background: "rgba(15,23,42,0.72)",
                        color: "#fff",
                        cursor: "pointer",
                        lineHeight: 1,
                        fontSize: "14px",
                      }}
                      aria-label={t("action.removeAttachment", { name: attachment.file.name })}
                      title={t("action.removeItem", { name: attachment.file.name })}
                    >
                      ×
                    </button>
                  </div>
                ))}
            </div>
          ) : null}
          <div style={{ display: "flex", flexWrap: "wrap", gap: "6px" }}>
            {pendingAttachments
              .filter((attachment) => !attachment.isImage)
              .map((attachment) => (
                <span
                  key={attachment.id}
                  style={{
                    display: "inline-flex",
                    alignItems: "center",
                    gap: "6px",
                    maxWidth: "220px",
                    padding: "4px 8px",
                    borderRadius: "999px",
                    background: isDark ? rgba(0.14) : rgba(0.08),
                    color: "var(--text-primary)",
                    fontSize: "12px",
                  }}
                >
                  <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{attachment.file.name}</span>
                  <button
                    type="button"
                    onClick={() => onRemoveAttachment(attachment.id)}
                    style={{
                      border: "none",
                      background: "transparent",
                      color: "var(--text-secondary)",
                      cursor: "pointer",
                      padding: 0,
                      lineHeight: 1,
                      fontSize: "14px",
                    }}
                    aria-label={t("action.removeAttachment", { name: attachment.file.name })}
                    title={t("action.removeItem", { name: attachment.file.name })}
                  >
                    ×
                  </button>
                </span>
              ))}
              <CompactUploadProgress
                progress={uploadProgress ?? null}
                label={t("upload.attachmentsProgress")}
                statusLabel={t("upload.inProgress")}
                cancelLabel={t("upload.cancel")}
                onCancel={onCancelUpload}
              />
            </div>
          </div>
        ) : null}
    </>
  );
}
