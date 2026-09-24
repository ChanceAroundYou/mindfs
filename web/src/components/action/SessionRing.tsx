import React, { useCallback, useEffect, useRef, useState } from "react";
import { useI18n } from "../../i18n";
import { appAccentHexToRgba } from "./styleHelpers";
import type { SessionInfo } from "./types";

/** 输入框右侧的会话环：点按打开会话抽屉，向左拖到底新开会话。拖拽状态内聚在这里。 */
export function SessionRing({
  hasBoundSession,
  canOpenSessionDrawer,
  sessionDrawerOpen,
  detachedBoundSession,
  accentColorRaw,
  accentHex,
  currentSession,
  onSessionClick,
  onDraggingChange,
  onNewSession,
  onResetForNewSession,
}: {
  hasBoundSession: boolean;
  canOpenSessionDrawer: boolean;
  sessionDrawerOpen: boolean;
  detachedBoundSession: boolean;
  accentColorRaw: string;
  accentHex: string;
  currentSession: SessionInfo | null;
  onSessionClick: () => void;
  onDraggingChange?: (dragging: boolean) => void;
  onNewSession?: () => void;
  onResetForNewSession?: () => void;
}) {
  const { t } = useI18n();
  const DRAG_THRESHOLD = -40;
  const [dragX, setDragX] = useState(0);
  const [isDragging, setIsDragging] = useState(false);
  const dragStartRef = useRef(0);

  const boundRingColor = detachedBoundSession ? "#f59e0b" : accentHex;
  const boundRingShadow = detachedBoundSession
    ? "0 0 0 1px rgba(245,158,11,0.18)"
    : `0 0 0 1px ${appAccentHexToRgba(accentHex, 0.08)}`;
  const boundArrowColor = detachedBoundSession ? "#f59e0b" : accentHex;

  const finishDrag = useCallback(() => {
    if (!isDragging) return;
    if (dragX <= DRAG_THRESHOLD) {
      onResetForNewSession?.();
      onNewSession?.();
    }
    setDragX(0);
    setIsDragging(false);
  }, [isDragging, dragX, onNewSession, onResetForNewSession]);

  const handleDragStart = (e: React.MouseEvent | React.TouchEvent) => {
    const clientX = "touches" in e ? e.touches[0].clientX : e.clientX;
    dragStartRef.current = clientX;
    setIsDragging(true);
  };

  useEffect(() => {
    onDraggingChange?.(isDragging);
    return () => onDraggingChange?.(false);
  }, [isDragging, onDraggingChange]);

  useEffect(() => {
    if (!isDragging) return;
    const move = (e: MouseEvent | TouchEvent) => {
      const clientX = "touches" in e ? e.touches[0].clientX : e.clientX;
      setDragX(Math.min(0, clientX - dragStartRef.current));
    };
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", finishDrag);
    window.addEventListener("touchmove", move);
    window.addEventListener("touchend", finishDrag);
    return () => {
      window.removeEventListener("mousemove", move);
      window.removeEventListener("mouseup", finishDrag);
      window.removeEventListener("touchmove", move);
      window.removeEventListener("touchend", finishDrag);
    };
  }, [isDragging, finishDrag]);

  return (
    <div
      data-onboarding="session-ring"
      onMouseDown={handleDragStart}
      onTouchStart={handleDragStart}
      onClick={() => {
        if (Math.abs(dragX) < 5) {
          onSessionClick?.();
        }
      }}
      style={{
        width: "32px",
        height: "32px",
        cursor: "pointer",
        transform: `translateX(${dragX}px)`,
        transition: isDragging ? "none" : "all 0.3s cubic-bezier(0.4, 0, 0.2, 1)",
        position: "relative",
        zIndex: 10,
        opacity: 1,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        touchAction: "none",
      }}
      title={t("action.swipeNewSession")}
    >
      {!hasBoundSession ? (
        <div
          style={{
            width: "14px",
            height: "14px",
            borderRadius: "50%",
            background: "transparent",
            border: "2px solid #94a3b8",
          }}
        />
      ) : (
        <div
          style={{
            width: "14px",
            height: "14px",
            borderRadius: "50%",
            background: "transparent",
            border: `2px solid ${boundRingColor}`,
            boxShadow: boundRingShadow,
          }}
        />
      )}
      {canOpenSessionDrawer ? (
        <svg
          width="12"
          height="12"
          viewBox="0 0 12 12"
          fill="none"
          style={{
            position: "absolute",
            inset: 0,
            margin: "auto",
            color: boundArrowColor,
            pointerEvents: "none",
          }}
          aria-hidden="true"
        >
          <path
            d={sessionDrawerOpen ? "M3.25 4.75 6 7.5l2.75-2.75" : "M3.25 7.25 6 4.5l2.75 2.75"}
            stroke="currentColor"
            strokeWidth="1.8"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      ) : null}
      {isDragging && dragX < -10 ? (
        <div style={{ position: "absolute", right: "100%", top: "50%", transform: "translateY(-50%)", marginRight: "8px", fontSize: "10px", fontWeight: 600, color: dragX <= DRAG_THRESHOLD ? accentColorRaw : "#9ca3af", whiteSpace: "nowrap", opacity: Math.min(1, Math.abs(dragX) / 20), pointerEvents: "none" }}>
          {dragX <= DRAG_THRESHOLD ? t("action.releaseNewSession") : t("action.swipeNewSession")}
        </div>
      ) : null}
    </div>
  );
}
