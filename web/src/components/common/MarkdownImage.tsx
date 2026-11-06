import React, { useState } from "react";
import { isDirectImageSrc, resolveMarkdownHref } from "../common/markdownUtils";
import { useProtectedBlobUrl } from "../../hooks/useProtectedBlobUrl";
import { ImageZoomOverlay } from "../common/ImageZoomOverlay";
import { useI18n } from "../../i18n/index";

export function MarkdownImage({
  src = "",
  alt = "",
  title,
  width,
  currentPath = "",
  root,
}: {
  src?: string;
  alt?: string;
  title?: string;
  width?: number | string;
  currentPath?: string;
  root?: string;
}) {
  const { t } = useI18n();
  const [zoomed, setZoomed] = useState(false);
  const trimmedSrc = src.trim();
  const directSrc = isDirectImageSrc(trimmedSrc);
  const resolvedPath = trimmedSrc && root && !directSrc
    ? resolveMarkdownHref(currentPath, trimmedSrc)
    : "";
  const protectedSrc = useProtectedBlobUrl(
    root && resolvedPath ? { rootId: root, path: resolvedPath } : undefined,
  );
  const resolvedSrc = directSrc
    ? trimmedSrc
    : !root
      ? trimmedSrc
      : resolvedPath
        ? protectedSrc
        : "";

  if (!resolvedSrc) {
    return (
      <span
        style={{
          display: "inline-block",
          color: "var(--text-secondary)",
          fontSize: "13px",
        }}
      >
        {alt || title || src}
      </span>
    );
  }

  return (
    <>
      <img
        src={resolvedSrc}
        alt={alt}
        title={title || t("imageViewer.expand")}
        width={width}
        style={{ cursor: "zoom-in" }}
        onClick={(event) => {
          // 图片可能被包在链接里（[![img](a)](b)）：点图是「我要看大图」，
          // 不该顺带跳走，所以把这次点击从链接上摘下来。
          event.preventDefault();
          event.stopPropagation();
          setZoomed(true);
        }}
      />
      {zoomed && (
        <ImageZoomOverlay src={resolvedSrc} alt={alt || title || ""} onClose={() => setZoomed(false)} />
      )}
    </>
  );
}
