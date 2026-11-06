import React, { memo, useState } from "react";
import { useI18n } from "../../i18n/index";
import { usePinchZoom } from "../../hooks/usePinchZoom";
import { useProtectedBlobUrl } from "../../hooks/useProtectedBlobUrl";
import { ImageZoomOverlay } from "../common/ImageZoomOverlay";

type ImageViewerProps = {
  path: string;
  root?: string;
  /** 文件版本（mtime）：变了就换一张图，不吃旧 blob 缓存。 */
  version?: string;
};

const toolbarStyle: React.CSSProperties = {
  position: "absolute",
  top: 4,
  right: 4,
  zIndex: 1,
  display: "flex",
  gap: 2,
  padding: 2,
  borderRadius: 8,
  background: "var(--panel-bg, #fff)",
  boxShadow: "0 2px 8px rgba(31, 37, 48, 0.12)",
};

const toolbarButtonStyle: React.CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  minWidth: 26,
  height: 26,
  padding: "0 5px",
  border: "none",
  borderRadius: 6,
  background: "transparent",
  color: "var(--text-primary)",
  font: "inherit",
  fontSize: 12,
  cursor: "pointer",
};

function ImageViewerInner({ path, root, version }: ImageViewerProps) {
  const { t } = useI18n();
  const [fullscreen, setFullscreen] = useState(false);
  const url = useProtectedBlobUrl(root ? { rootId: root, path, version } : undefined);
  const { transform, zoomed, zoomBy, reset, surfaceRef, bind } = usePinchZoom(Boolean(url));

  return (
    <div style={{ position: "relative", display: "flex", flex: 1, minHeight: 0 }}>
      <div
        ref={surfaceRef}
        {...bind}
        style={{
          display: "flex",
          flex: 1,
          minHeight: 0,
          overflow: "hidden",
          justifyContent: "center",
          alignItems: "center",
          padding: "24px",
          // 未缩放时把手势还给页面滚动（会话/文件区可上下滚）；一旦放大或平移，
          // 整块都归图片，否则拖动会连带滚动父容器。
          touchAction: zoomed ? "none" : "pan-y",
          userSelect: "none",
          cursor: url ? (zoomed ? "grab" : "zoom-in") : "default",
        }}
      >
        {url ? (
          <img
            src={url}
            alt={path}
            draggable={false}
            style={{
              maxWidth: "100%",
              maxHeight: "100%",
              borderRadius: "12px",
              boxShadow: "0 12px 24px rgba(31, 37, 48, 0.1)",
              transform: `translate(${transform.x}px, ${transform.y}px) scale(${transform.scale})`,
            }}
          />
        ) : (
          <div style={{ color: "var(--text-secondary)" }}>{t("fileViewer.previewLoading")}</div>
        )}
      </div>
      {url && (
        <div style={toolbarStyle}>
          <button
            type="button"
            style={toolbarButtonStyle}
            title={t("imageViewer.zoomOut")}
            aria-label={t("imageViewer.zoomOut")}
            disabled={transform.scale <= 0.25}
            onClick={() => zoomBy(1 / 1.5)}
          >
            −
          </button>
          <button
            type="button"
            style={toolbarButtonStyle}
            title={t("imageViewer.reset")}
            aria-label={t("imageViewer.reset")}
            onClick={reset}
          >
            {Math.round(transform.scale * 100)}%
          </button>
          <button
            type="button"
            style={toolbarButtonStyle}
            title={t("imageViewer.zoomIn")}
            aria-label={t("imageViewer.zoomIn")}
            disabled={transform.scale >= 16}
            onClick={() => zoomBy(1.5)}
          >
            +
          </button>
          <button
            type="button"
            style={toolbarButtonStyle}
            title={t("imageViewer.expand")}
            aria-label={t("imageViewer.expand")}
            onClick={() => setFullscreen(true)}
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M9 3H3v6M15 3h6v6M9 21H3v-6M21 15v6h-6" />
            </svg>
          </button>
        </div>
      )}
      {fullscreen && url && (
        <ImageZoomOverlay src={url} alt={path} onClose={() => setFullscreen(false)} />
      )}
    </div>
  );
}

export const ImageViewer = memo(ImageViewerInner, (prev, next) => (
  prev.path === next.path && prev.root === next.root && prev.version === next.version
));
