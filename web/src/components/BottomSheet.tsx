import React, { useEffect, useRef, useState } from "react";
import {
  BOTTOM_SHEET_DRAG_START_PX,
  resolveBottomSheetRelease,
} from "../services/bottomSheetModel";

type BottomSheetProps = {
  isOpen: boolean;
  onClose: () => void;
  children: React.ReactNode;
  footer?: React.ReactNode;
  onExpand?: () => void;
  contentRef?: React.RefObject<HTMLDivElement | null>;
};

export function BottomSheet({
  isOpen,
  onClose,
  children,
  footer,
  onExpand,
  contentRef,
}: BottomSheetProps) {
  const [isAnimating, setIsAnimating] = useState(false);
  const [isMobile, setIsMobile] = useState(window.innerWidth < 768);
  const startYRef = useRef(0);
  const lastYRef = useRef(0);
  const pointerIdRef = useRef<number | null>(null);
  const [dragOffsetY, setDragOffsetY] = useState(0);
  const [isDragging, setIsDragging] = useState(false);

  useEffect(() => {
    const handleResize = () => setIsMobile(window.innerWidth < 768);
    window.addEventListener("resize", handleResize);
    if (isOpen) setIsAnimating(true);
    return () => window.removeEventListener("resize", handleResize);
  }, [isOpen]);

  if (!isOpen && !isAnimating) return null;

  const handlePointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
    pointerIdRef.current = event.pointerId;
    startYRef.current = event.clientY;
    lastYRef.current = event.clientY;
    setIsDragging(false);
    event.currentTarget.setPointerCapture(event.pointerId);
  };

  const handlePointerMove = (event: React.PointerEvent<HTMLDivElement>) => {
    if (pointerIdRef.current !== event.pointerId) return;
    const deltaY = event.clientY - startYRef.current;
    if (!isDragging && Math.abs(deltaY) >= BOTTOM_SHEET_DRAG_START_PX) {
      setIsDragging(true);
    }
    if (Math.abs(deltaY) >= BOTTOM_SHEET_DRAG_START_PX) {
      lastYRef.current = event.clientY;
      setDragOffsetY(deltaY);
    }
  };

  const finishPointer = (
    event: React.PointerEvent<HTMLDivElement>,
    cancelled: boolean,
  ) => {
    if (pointerIdRef.current !== event.pointerId) return;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    const release = cancelled
      ? "half"
      : resolveBottomSheetRelease({
          clientY: lastYRef.current,
          viewportHeight: window.innerHeight,
          dragged: isDragging,
        });
    pointerIdRef.current = null;
    setDragOffsetY(0);
    setIsDragging(false);
    if (release === "expand") onExpand?.();
    if (release === "close") onClose();
  };

  const handlePointerUp = (event: React.PointerEvent<HTMLDivElement>) => finishPointer(event, false);
  const handlePointerCancel = (event: React.PointerEvent<HTMLDivElement>) => finishPointer(event, true);

  const dragTransform = isDragging ? `translateY(${dragOffsetY}px)` : undefined;

  const pcStyles: React.CSSProperties = {
    position: "absolute",
    left: 0,
    right: 0,
    bottom: 0,
    width: "100%",
    height: "75vh",
    borderRadius: "16px 16px 0 0",
    opacity: isOpen ? 1 : 0,
    pointerEvents: isOpen ? "auto" : "none",
    transform: dragTransform ?? (isOpen ? "translateY(0)" : "translateY(20px)"),
  };

  const mobileStyles: React.CSSProperties = {
    position: "fixed",
    left: 0,
    right: 0,
    bottom: 0,
    width: "100%",
    height: "75vh",
    borderTopLeftRadius: "20px",
    borderTopRightRadius: "20px",
    transform: dragTransform ?? (isOpen ? "translateY(0)" : "translateY(100%)"),
  };

  return (
    <>
      {/* Overlay: 点击抽屉外任意区域关闭（包含空白、文件区、会话列表区） */}
      <div
        style={{
          position: "fixed",
          inset: 0,
          background: isMobile ? "rgba(0,0,0,0.3)" : "transparent",
          zIndex: 1000,
          opacity: isOpen ? 1 : 0,
          transition: "opacity 0.3s ease-out",
          pointerEvents: isOpen ? "auto" : "none",
        }}
        onClick={onClose}
      />

      {/* Drawer Panel */}
      <div
        style={{
          background: "var(--panel-bg, #ffffff)",
          color: "var(--text-primary)",
          boxShadow: "0 -4px 24px rgba(0,0,0,0.08)",
          borderTop: "1px solid rgba(148, 163, 184, 0.22)",
          zIndex: 1001,
          display: "flex",
          flexDirection: "column",
          transition: isDragging ? "none" : "all 0.3s cubic-bezier(0.4, 0, 0.2, 1)",
          overflow: "hidden",
          border: "none",
          ...(isMobile ? mobileStyles : pcStyles),
        }}
        onTransitionEnd={() => {
          if (!isOpen) setIsAnimating(false);
        }}
      >
        {/* Handle Area (Compressed) */}
        <div
          style={{
            width: "100%",
            height: "8px",
            display: "flex",
            justifyContent: "center",
            alignItems: "center",
            cursor: "ns-resize",
            flexShrink: 0,
          }}
          onPointerDown={handlePointerDown}
          onPointerMove={handlePointerMove}
          onPointerUp={handlePointerUp}
          onPointerCancel={handlePointerCancel}
        >
          <div style={{ width: "64px", height: "3px", background: "#2563eb", borderRadius: "999px" }} />
        </div>

        {/* Content */}
        <div ref={contentRef} style={{ flex: 1, overflow: "auto", WebkitOverflowScrolling: "touch", minHeight: 0 }}>
          {children}
        </div>

        {/* Optional Footer */}
        {footer && (
          <div style={{ borderTop: "1px solid var(--border-color)", background: "var(--panel-bg, #ffffff)" }}>
            {footer}
          </div>
        )}
      </div>
    </>
  );
}
