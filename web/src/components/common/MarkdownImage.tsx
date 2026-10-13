import React, { useEffect, useState } from "react";
import { fetchProofProtectedBlob } from "../../services/file";
import { isDirectImageSrc, resolveMarkdownHref } from "../common/markdownUtils";

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
  const [resolvedSrc, setResolvedSrc] = useState("");

  useEffect(() => {
    let cancelled = false;
    let objectURL = "";

    async function loadImage() {
      const trimmedSrc = src.trim();
      if (!trimmedSrc) {
        setResolvedSrc("");
        return;
      }

      if (isDirectImageSrc(trimmedSrc)) {
        setResolvedSrc(trimmedSrc);
        return;
      }

      if (!root) {
        setResolvedSrc(trimmedSrc);
        return;
      }

      try {
        const resolvedPath = resolveMarkdownHref(currentPath, trimmedSrc);
        if (!resolvedPath) {
          setResolvedSrc("");
          return;
        }
        const blob = await fetchProofProtectedBlob({ rootId: root, path: resolvedPath });
        if (cancelled) return;
        objectURL = URL.createObjectURL(blob);
        setResolvedSrc(objectURL);
      } catch {
        if (!cancelled) {
          setResolvedSrc("");
        }
      }
    }

    void loadImage();

    return () => {
      cancelled = true;
      if (objectURL) {
        URL.revokeObjectURL(objectURL);
      }
    };
  }, [currentPath, root, src]);

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
