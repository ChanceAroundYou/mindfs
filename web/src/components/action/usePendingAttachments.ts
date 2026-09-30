import { useCallback, useEffect, useRef, useState } from "react";
import { type PendingAttachment } from "./AttachmentsArea";
import { buildPendingAttachment } from "./modelUtils";

/** 待发送附件：预览 URL 生命周期（创建/移除/卸载时 revoke）内聚在这里。 */
export function usePendingAttachments() {
  const [pendingAttachments, setPendingAttachments] = useState<PendingAttachment[]>([]);
  const pendingAttachmentsRef = useRef<PendingAttachment[]>([]);
  pendingAttachmentsRef.current = pendingAttachments;

  useEffect(() => {
    return () => {
      pendingAttachmentsRef.current.forEach((attachment: PendingAttachment) => {
        if (attachment.previewUrl) {
          URL.revokeObjectURL(attachment.previewUrl);
        }
      });
    };
  }, []);

  const appendPendingAttachments = useCallback((files: File[]) => {
    if (files.length === 0) {
      return;
    }
    setPendingAttachments((prev) => [...prev, ...files.map(buildPendingAttachment)]);
  }, []);

  const removePendingAttachment = useCallback((targetId: string) => {
    setPendingAttachments((prev) => {
      const target = prev.find((item) => item.id === targetId);
      if (target?.previewUrl) {
        URL.revokeObjectURL(target.previewUrl);
      }
      return prev.filter((item) => item.id !== targetId);
    });
  }, []);

  const clearPendingAttachments = useCallback(() => {
    setPendingAttachments((prev) => {
      prev.forEach((attachment) => {
        if (attachment.previewUrl) {
          URL.revokeObjectURL(attachment.previewUrl);
        }
      });
      return [];
    });
  }, []);

  return {
    pendingAttachments,
    appendPendingAttachments,
    removePendingAttachment,
    clearPendingAttachments,
  };
}
