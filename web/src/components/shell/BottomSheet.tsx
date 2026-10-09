import React from "react";
import { useBottomSheet } from "./useBottomSheet";

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
  const {
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
  } = useBottomSheet({ isOpen, onClose, onExpand });

  if (!isOpen && !isAnimating) return null;

  const defaultHeight = "70%";
  const computedHeight = sheetHeightPx != null ? `${sheetHeightPx}px` : defaultHeight;

  const pcStyles: React.CSSProperties = {
    position: "absolute",
    left: 0,
    right: 0,
    bottom: 0,
    width: "100%",
    height: computedHeight,
    borderRadius: "16px 16px 0 0",
    opacity: isOpen ? 1 : 0,
    pointerEvents: isOpen ? "auto" : "none",
    transform: isOpen ? "translateY(0)" : "translateY(20px)",
  };

  const mobileStyles: React.CSSProperties = {
    position: "fixed",
    left: 0,
    right: 0,
    bottom: 0,
    width: "100%",
    height: computedHeight,
    borderTopLeftRadius: "20px",
    borderTopRightRadius: "20px",
    transform: isOpen ? "translateY(0)" : "translateY(100%)",
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
        ref={sheetRef}
        style={{
          background: "var(--panel-bg, #ffffff)",
          color: "var(--text-primary)",
          boxShadow: "0 -4px 24px rgba(0,0,0,0.08)",
          borderTop: "1px solid rgba(148, 163, 184, 0.22)",
          zIndex: 1001,
          display: "flex",
          flexDirection: "column",
          transition: isDragging
            ? "none"
            : "height 0.24s cubic-bezier(0.4, 0, 0.2, 1), transform 0.24s cubic-bezier(0.4, 0, 0.2, 1), opacity 0.24s cubic-bezier(0.4, 0, 0.2, 1)",
          willChange: isDragging ? "height" : undefined,
          contain: "layout paint",
          overflow: "hidden",
          border: "none",
          ...(isMobile ? mobileStyles : pcStyles),
        }}
        onTransitionEnd={() => {
          if (!isOpen) setIsAnimating(false);
        }}
      >
        {/* Handle Area */}
        <div
          style={{
            width: "100%",
            height: "16px",
            display: "flex",
            justifyContent: "center",
            alignItems: "center",
            cursor: "ns-resize",
            touchAction: "none",
            flexShrink: 0,
          }}
          onPointerDown={handlePointerDown}
          onPointerMove={handlePointerMove}
          onPointerUp={handlePointerUp}
          onPointerCancel={handlePointerCancel}
          title="双击全屏"
          onDoubleClick={() => {
            if (!isDragging) onExpand?.();
          }}
        >
          <div style={{ width: "96px", height: "5px", background: "var(--accent-color)", borderRadius: "999px" }} />
        </div>

        {/* Content */}
        <div ref={contentRef} style={{ flex: 1, display: "flex", overflow: "hidden", WebkitOverflowScrolling: "touch", minHeight: 0 }}>
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
