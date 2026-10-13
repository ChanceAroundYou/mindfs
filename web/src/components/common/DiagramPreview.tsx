import React, { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../../i18n/index";
import { ExitIcon } from "../common/ExitIcon";
import { diagramGesture, zoomDiagramAt, type DiagramPoint, type DiagramTransform } from "../../shared/diagramZoom";

const DIAGRAM_PREVIEW_STYLES = String.raw`.diagram-preview {
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
.diagram-preview[open] { display: flex; flex-direction: column; }
.diagram-preview::backdrop { background: rgba(0, 0, 0, 0.5); }
.diagram-preview-toolbar {
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
.diagram-preview-toolbar strong { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.diagram-preview-controls { display: flex; gap: 4px; flex-shrink: 0; }
.diagram-preview-controls button {
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
.diagram-preview-controls button:disabled { opacity: 0.4; cursor: default; }
.diagram-preview-controls button:focus-visible, .diagram-expand:focus-visible { outline: 2px solid var(--accent-color); outline-offset: 2px; }
.diagram-preview-viewport {
  position: relative;
  flex: 1;
  min-height: 0;
  overflow: hidden;
  touch-action: none;
  user-select: none;
  cursor: grab;
  background: #fff;
}
.diagram-preview-viewport:active { cursor: grabbing; }
.diagram-preview-position { position: absolute; left: 50%; top: 50%; transform-origin: 0 0; }
.diagram-preview-drawing { transform: translate(-50%, -50%); pointer-events: none; }
.diagram-preview-drawing svg { display: block; }
.diagram-preview-hint {
  padding: 8px max(12px, env(safe-area-inset-right, 0px)) calc(max(var(--mindfs-safe-area-bottom, 0px), env(safe-area-inset-bottom, 0px)) + 8px) max(12px, env(safe-area-inset-left, 0px));
  flex-shrink: 0;
  text-align: center;
  font-size: 12px;
  color: var(--text-secondary);
  background: var(--panel-bg, #fff);
  border-top: 1px solid var(--panel-border, var(--border-color));
}
.diagram-expand {
  position: absolute;
  top: 4px;
  right: 4px;
  z-index: 1;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: #2563eb;
  opacity: 0.5;
  cursor: pointer;
}
.diagram-expand:hover, .diagram-expand:focus-visible { opacity: 1; }`;

export function DiagramPreview({ svg, onClose }: { svg: string; onClose: () => void }) {
  const { t } = useI18n();
  const dialog = useRef<HTMLDialogElement>(null);
  const viewport = useRef<HTMLDivElement>(null);
  const drawing = useRef<HTMLDivElement>(null);
  const points = useRef(new Map<number, DiagramPoint>());
  const [transform, setTransform] = useState<DiagramTransform>({ x: 0, y: 0, scale: 1 });

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

  useEffect(() => {
    const element = drawing.current?.querySelector("svg");
    const surface = viewport.current;
    if (!element || !surface) return;
    const viewBox = element.viewBox.baseVal;
    const width = viewBox.width || element.getBoundingClientRect().width || 800;
    const height = viewBox.height || element.getBoundingClientRect().height || 600;
    const fit = () => {
      const scale = Math.min((surface.clientWidth - 32) / width, (surface.clientHeight - 32) / height);
      const fittedWidth = Math.max(1, width * scale);
      const fittedHeight = Math.max(1, height * scale);
      element.style.width = `${fittedWidth}px`;
      element.style.height = `${fittedHeight}px`;
      element.style.maxWidth = "none";
      drawing.current!.style.width = `${fittedWidth}px`;
      drawing.current!.style.height = `${fittedHeight}px`;
      points.current.clear();
      setTransform({ x: 0, y: 0, scale: 1 });
    };
    const observer = new ResizeObserver(fit);
    observer.observe(surface);
    fit();
    return () => observer.disconnect();
  }, [svg]);

  const localPoint = (event: { clientX: number; clientY: number }): DiagramPoint => {
    const rect = viewport.current!.getBoundingClientRect();
    return { x: event.clientX - rect.left - rect.width / 2, y: event.clientY - rect.top - rect.height / 2 };
  };
  const zoom = (factor: number) => setTransform(current => zoomDiagramAt(current, factor, { x: 0, y: 0 }));
  const release = (event: React.PointerEvent) => { points.current.delete(event.pointerId); };

  useEffect(() => {
    const surface = viewport.current!;
    const wheel = (event: WheelEvent) => {
      event.preventDefault();
      const factor = Math.exp(-Math.max(-100, Math.min(100, event.deltaY)) * 0.01);
      setTransform(current => zoomDiagramAt(current, factor, localPoint(event)));
    };
    surface.addEventListener("wheel", wheel, { passive: false });
    return () => surface.removeEventListener("wheel", wheel);
  }, []);

  return createPortal(
    <>
      <style>{DIAGRAM_PREVIEW_STYLES}</style>
      <dialog ref={dialog} className="diagram-preview" aria-label={t("diagram.preview")} onCancel={event => { event.preventDefault(); onClose(); }}>
      <div className="diagram-preview-toolbar">
        <strong>{t("diagram.preview")}</strong>
        <div className="diagram-preview-controls">
          <button type="button" title={t("diagram.zoomOut")} aria-label={t("diagram.zoomOut")} disabled={transform.scale <= 0.25} onClick={() => zoom(1 / 1.5)}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden="true">
              <circle cx="10.5" cy="10.5" r="7.5" /><path d="m16 16 5 5M7 10.5h7" />
            </svg>
          </button>
          <button type="button" title={t("diagram.reset")} aria-label={t("diagram.reset")} onClick={() => setTransform({ x: 0, y: 0, scale: 1 })}>{Math.round(transform.scale * 100)}%</button>
          <button type="button" title={t("diagram.zoomIn")} aria-label={t("diagram.zoomIn")} disabled={transform.scale >= 16} onClick={() => zoom(1.5)}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden="true">
              <circle cx="10.5" cy="10.5" r="7.5" /><path d="m16 16 5 5M7 10.5h7M10.5 7v7" />
            </svg>
          </button>
          <button type="button" title={t("common.close")} aria-label={t("common.close")} onClick={onClose} autoFocus><ExitIcon /></button>
        </div>
      </div>
      <div ref={viewport} className="diagram-preview-viewport"
        onPointerDown={event => {
          if (event.pointerType === "mouse" && event.button !== 0) return;
          event.currentTarget.setPointerCapture(event.pointerId);
          points.current.set(event.pointerId, localPoint(event));
        }}
        onPointerMove={event => {
          if (!points.current.has(event.pointerId)) return;
          const before = diagramGesture([...points.current.values()]);
          points.current.set(event.pointerId, localPoint(event));
          const after = diagramGesture([...points.current.values()]);
          const factor = before.distance > 0 && after.distance > 0 ? after.distance / before.distance : 1;
          setTransform(current => zoomDiagramAt(current, factor, before.center, after.center));
        }}
        onPointerUp={release} onPointerCancel={release} onLostPointerCapture={release}
        onDoubleClick={() => setTransform(current => current.scale > 1 ? { x: 0, y: 0, scale: 1 } : zoomDiagramAt(current, 2, { x: 0, y: 0 }))}
      >
        <div className="diagram-preview-position" style={{ transform: `translate(${transform.x}px, ${transform.y}px) scale(${transform.scale})` }}>
          <div ref={drawing} className="diagram-preview-drawing" dangerouslySetInnerHTML={{ __html: svg }} />
        </div>
      </div>
      <div className="diagram-preview-hint">{t("diagram.gestures")}</div>
      </dialog>
    </>, document.body,
  );
}
