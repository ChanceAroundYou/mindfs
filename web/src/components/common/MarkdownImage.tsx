import React from "react";
import { isDirectImageSrc, resolveMarkdownHref } from "../common/markdownUtils";
import { useProtectedBlobUrl } from "../../hooks/useProtectedBlobUrl";

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

  return <img src={resolvedSrc} alt={alt} title={title} width={width} />;
}
