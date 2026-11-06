/**
 * 全屏图片灯箱：`<dialog showModal>` + portal，双指/滚轮缩放、拖动、双击复位。
 *
 * 结构照 `DiagramPreview`（同一个可访问性套路：Escape 关闭、锁 body 滚动、
 * 关闭后把焦点还给打开前的元素），但 fit 按图片自然尺寸算 —— 图表那边是看
 * SVG viewBox，两者取数方式不同，所以不复用它的组件、只共用缩放数学。
 *
 * 消费方：文件视图的 ImageViewer（右上角「放大查看」）与会话里的 MarkdownImage（点图）。
 */
import React, { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../../i18n/index";
import { usePinchZoom } from "../../hooks/usePinchZoom";
import { ExitIcon } from "../common/ExitIcon";

const IMAGE_ZOOM_STYLES = String.raw`.image-zoom {
  position: fixed;
  inset: 0;
  width: 100%;
  height: 100%;
  height: 100dvh;
  max-width: none;
  max-height: none;
  margin: 0;
  padding: 0;
  border: 0;
  box-sizing: border-box;
  background: var(--panel-bg, #fff);
  color: var(--text-primary);
  overscroll-behavior: contain;
}
.image-zoom[open] { display: flex; flex-direction: column; }
.image-zoom::backdrop { background: rgba(0, 0, 0, 0.6); }
.image-zoom-toolbar {
  display: flex;
  flex-wrap: nowrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: calc(max(var(--mindfs-safe-area-top, 0px), env(safe-area-inset-top, 0px)) + 4px) max(12px, env(safe-area-inset-right, 0px)) 4px max(12px, env(safe-area-inset-left, 0px));
  flex-shrink: 0;
  background: var(--panel-bg, #fff);
  border-bottom: 1px solid var(--panel-border, var(--border-color));
  font-size: 13px;
}
.image-zoom-toolbar strong { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.image-zoom-controls { display: flex; gap: 4px; flex-shrink: 0; }
.image-zoom-controls button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: inherit;
  min-width: 32px;
  height: 32px;
  padding: 0 6px;
  cursor: pointer;
  font: inherit;
}
.image-zoom-controls button:disabled { opacity: 0.4; cursor: default; }
.image-zoom-controls button:focus-visible { outline: 2px solid var(--accent-color); outline-offset: 2px; }
.image-zoom-viewport {
  position: relative;
  flex: 1;
  min-height: 0;
  overflow: hidden;
  touch-action: none;
  user-select: none;
  cursor: grab;
  background: var(--panel-bg, #fff);
  background-image: linear-gradient(45deg, rgba(0, 0, 0, 0.03) 25%, transparent 25%, transparent 75%, rgba(0, 0, 0, 0.03) 75%),
    linear-gradient(45deg, rgba(0, 0, 0, 0.03) 25%, transparent 25%, transparent 75%, rgba(0, 0, 0, 0.03) 75%);
  background-size: 16px 16px;
  background-position: 0 0, 8px 8px;
}
.image-zoom-viewport:active { cursor: grabbing; }
.image-zoom-position { position: absolute; left: 50%; top: 50%; transform-origin: 0 0; }
.image-zoom-drawing { transform: translate(-50%, -50%); pointer-events: none; }
.image-zoom-drawing img { display: block; max-width: none; }
.image-zoom-hint {
  padding: 8px max(12px, env(safe-area-inset-right, 0px)) calc(max(var(--mindfs-safe-area-bottom, 0px), env(safe-area-inset-bottom, 0px)) + 8px) max(12px, env(safe-area-inset-left, 0px));
  flex-shrink: 0;
  text-align: center;
  font-size: 12px;
  color: var(--text-secondary);
  background: var(--panel-bg, #fff);
  border-top: 1px solid var(--panel-border, var(--border-color));
}`;

export type ImageZoomOverlayProps = {
  src: string;
  alt?: string;
  onClose: () => void;
};

export function ImageZoomOverlay({ src, alt = "", onClose }: ImageZoomOverlayProps) {
  const { t } = useI18n();
  const dialog = useRef<HTMLDialogElement>(null);
  const image = useRef<HTMLImageElement>(null);
  const [viewport, setViewport] = useState<HTMLDivElement | null>(null);
  const [fitSize, setFitSize] = useState<{ width: number; height: number } | null>(null);
  const zoom = usePinchZoom();
  const { reset, surfaceRef, zoomBy, transform } = zoom;

  const setViewportNode = useCallback((node: HTMLDivElement | null) => {
    surfaceRef(node);
    setViewport(node);
  }, [surfaceRef]);

  useEffect(() => {
    const node = dialog.current!;
    const previousFocus = document.activeElement;
    node.showModal();
    const overflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      node.close();
      document.body.style.overflow = overflow;
      if (previousFocus instanceof HTMLElement) previousFocus.focus();
    };
  }, []);

  const fit = useCallback(() => {
    const element = image.current;
    const box = viewport;
    if (!element || !box) return;
    const naturalWidth = element.naturalWidth || 0;
    const naturalHeight = element.naturalHeight || 0;
    if (naturalWidth <= 0 || naturalHeight <= 0) return;
    const scale = Math.min((box.clientWidth - 24) / naturalWidth, (box.clientHeight - 24) / naturalHeight);
    setFitSize({
      width: Math.max(1, naturalWidth * scale),
      height: Math.max(1, naturalHeight * scale),
    });
  }, [viewport]);

  // 换图（重开另一个 src）时重新 fit 并复位。
  useEffect(() => {
    setFitSize(null);
    reset();
  }, [reset, src]);

  useEffect(() => {
    if (!viewport) return undefined;
    const observer = new ResizeObserver(() => fit());
    observer.observe(viewport);
    return () => observer.disconnect();
  }, [fit, viewport]);

  return createPortal(
    <>
      <style>{IMAGE_ZOOM_STYLES}</style>
      <dialog
        ref={dialog}
        className="image-zoom"
        aria-label={alt || t("imageViewer.preview")}
        onCancel={(event) => { event.preventDefault(); onClose(); }}
      >
        <div className="image-zoom-toolbar">
          <strong>{alt || t("imageViewer.preview")}</strong>
          <div className="image-zoom-controls">
            <button
              type="button"
              title={t("imageViewer.zoomOut")}
              aria-label={t("imageViewer.zoomOut")}
              disabled={transform.scale <= 0.25}
              onClick={() => zoomBy(1 / 1.5)}
            >
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden="true">
                <circle cx="10.5" cy="10.5" r="7.5" /><path d="m16 16 5 5M7 10.5h7" />
              </svg>
            </button>
            <button type="button" title={t("imageViewer.reset")} aria-label={t("imageViewer.reset")} onClick={reset}>
              {Math.round(transform.scale * 100)}%
            </button>
            <button
              type="button"
              title={t("imageViewer.zoomIn")}
              aria-label={t("imageViewer.zoomIn")}
              disabled={transform.scale >= 16}
              onClick={() => zoomBy(1.5)}
            >
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden="true">
                <circle cx="10.5" cy="10.5" r="7.5" /><path d="m16 16 5 5M7 10.5h7M10.5 7v7" />
              </svg>
            </button>
            <button type="button" title={t("common.close")} aria-label={t("common.close")} onClick={onClose} autoFocus>
              <ExitIcon />
            </button>
          </div>
        </div>
        <div ref={setViewportNode} className="image-zoom-viewport" {...zoom.bind}>
          <div
            className="image-zoom-position"
            style={{ transform: `translate(${transform.x}px, ${transform.y}px) scale(${transform.scale})` }}
          >
            <div className="image-zoom-drawing">
              <img
                ref={image}
                src={src}
                alt={alt}
                draggable={false}
                style={fitSize ? { width: `${fitSize.width}px`, height: `${fitSize.height}px` } : { visibility: "hidden" }}
                onLoad={fit}
              />
            </div>
          </div>
        </div>
        <div className="image-zoom-hint">{t("imageViewer.gestures")}</div>
      </dialog>
    </>, document.body,
  );
}
