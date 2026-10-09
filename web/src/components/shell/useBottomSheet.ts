import React, { useEffect, useRef, useState } from "react";
import {
  BOTTOM_SHEET_DRAG_START_PX,
  resolveBottomSheetRelease,
} from "../../services/bottomSheetModel";

export function useBottomSheet({
  isOpen,
  onClose,
  onExpand,
}: {
  isOpen: boolean;
  onClose: () => void;
  onExpand?: () => void;
}) {
  const [isAnimating, setIsAnimating] = useState(false);
  const [isMobile, setIsMobile] = useState(window.innerWidth < 768);
  const startYRef = useRef(0);
  const lastYRef = useRef(0);
  const startHeightRef = useRef(0);
  const pointerIdRef = useRef<number | null>(null);
  const sheetRef = useRef<HTMLDivElement | null>(null);
  const [sheetHeightPx, setSheetHeightPx] = useState<number | null>(null);
  const [isDragging, setIsDragging] = useState(false);
  const pendingHeightRef = useRef<number | null>(null);
  const heightFrameRef = useRef<number | null>(null);

  useEffect(() => {
    const handleResize = () => setIsMobile(window.innerWidth < 768);
    window.addEventListener("resize", handleResize);
    if (isOpen) setIsAnimating(true);
    return () => {
      window.removeEventListener("resize", handleResize);
      if (heightFrameRef.current != null) {
        window.cancelAnimationFrame(heightFrameRef.current);
        heightFrameRef.current = null;
      }
    };
  }, [isOpen]);

  // Default height is 70% of the viewport (desktop and mobile unified).
  // When a custom height exists (after dragging), keep it until close/expand.
  useEffect(() => {
    if (!isOpen) {
      // Reset to default on close so next open starts at 70% again
      setSheetHeightPx(null);
      setIsDragging(false);
    }
  }, [isOpen]);

  const clampHeight = (h: number) => {
    const vh = window.innerHeight || 800;
    // Allow arbitrary hover but keep a usable range; avoid fully collapsed invisible sheet
    const min = 120;
    const max = Math.max(min + 1, vh - 24);
    return Math.min(max, Math.max(min, h));
  };

  const handlePointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
    pointerIdRef.current = event.pointerId;
    startYRef.current = event.clientY;
    lastYRef.current = event.clientY;
    const rectH = sheetRef.current?.getBoundingClientRect().height;
    // If no custom height yet, derive start height from rendered size (which is 70% default)
    startHeightRef.current = sheetHeightPx ?? rectH ?? window.innerHeight * 0.5;
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
      pendingHeightRef.current = clampHeight(startHeightRef.current - deltaY);
      if (heightFrameRef.current == null) {
        heightFrameRef.current = window.requestAnimationFrame(() => {
          heightFrameRef.current = null;
          if (pendingHeightRef.current != null) {
            setSheetHeightPx(pendingHeightRef.current);
            pendingHeightRef.current = null;
          }
        });
      }
    }
  };

  const finishPointer = (
    event: React.PointerEvent<HTMLDivElement>,
    cancelled: boolean,
  ) => {
    if (pointerIdRef.current !== event.pointerId) return;
    if (heightFrameRef.current != null) {
      window.cancelAnimationFrame(heightFrameRef.current);
      heightFrameRef.current = null;
    }
    pendingHeightRef.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    if (cancelled) {
      pointerIdRef.current = null;
      setIsDragging(false);
      setSheetHeightPx(null);
      return;
    }
    const release = resolveBottomSheetRelease({
      clientY: lastYRef.current,
      viewportHeight: window.innerHeight,
      dragged: isDragging,
    });
    pointerIdRef.current = null;
    setIsDragging(false);
    if (release === "expand") {
      setSheetHeightPx(null);
      onExpand?.();
      return;
    }
    if (release === "close") {
      setSheetHeightPx(null);
      onClose();
      return;
    }
    // Otherwise keep the arbitrary hover height (no snap back to 50%)
  };

  const handlePointerUp = (event: React.PointerEvent<HTMLDivElement>) => finishPointer(event, false);
  const handlePointerCancel = (event: React.PointerEvent<HTMLDivElement>) => finishPointer(event, true);

  return {
    isAnimating,
    setIsAnimating,
    isMobile,
    sheetHeightPx,
    isDragging,
    sheetRef,
    handlePointerDown,
    handlePointerMove,
    handlePointerUp,
    handlePointerCancel,
  };
}
