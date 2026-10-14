import React from "react";
import { useProtectedBlobUrl } from "../../hooks/useProtectedBlobUrl";
import type { UploadAttachment } from "./sessionViewerTypes";

export function AttachmentImage({
  rootId,
  path,
  name,
}: {
  rootId: string;
  path: string;
  name: string;
}) {
  const url = useProtectedBlobUrl(rootId && path ? { rootId, path } : undefined);

  return (
    <img
      src={url}
      alt={name}
      style={{
        display: "block",
        width: "100%",
        maxHeight: "220px",
        objectFit: "cover",
        background: "rgba(15,23,42,0.06)",
      }}
    />
  );
}
