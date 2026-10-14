import React, { memo } from "react";
import { useProtectedBlobUrl } from "../../hooks/useProtectedBlobUrl";

type ImageViewerProps = {
  path: string;
  root?: string;
};

function ImageViewerInner({ path, root }: ImageViewerProps) {
  const url = useProtectedBlobUrl(root ? { rootId: root, path } : undefined);
  return (
    <div
      style={{
        padding: "24px",
        display: "flex",
        flex: 1,
        minHeight: 0,
        justifyContent: "center",
        alignItems: "center",
      }}
    >
      {url ? (
        <img
          src={url}
          alt={path}
          style={{
            maxWidth: "100%",
            maxHeight: "100%",
            borderRadius: "12px",
            boxShadow: "0 12px 24px rgba(31, 37, 48, 0.1)",
          }}
        />
      ) : (
        <div style={{ color: "var(--text-secondary)" }}>Loading image...</div>
      )}
    </div>
  );
}

export const ImageViewer = memo(ImageViewerInner, (prev, next) => (
  prev.path === next.path && prev.root === next.root
));
