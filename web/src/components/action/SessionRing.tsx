import React, { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../../i18n";
import { sessionService, type Session } from "../../services/session";
import { recentQuickSwitchGroups, ringGesture } from "../../services/quickSwitch";
import { appAccentHexToRgba } from "./styleHelpers";
import type { SessionInfo } from "./types";

type QuickSwitchGroup = ReturnType<typeof recentQuickSwitchGroups>[number];

/** 输入框右侧的会话环：点按打开会话抽屉，左拖到底新开会话，上拖拉出快捷切换面板。拖拽状态内聚在这里。 */
export function SessionRing({
  hasBoundSession,
  canOpenSessionDrawer,
  sessionDrawerOpen,
  detachedBoundSession,
  accentColorRaw,
  accentHex,
  currentSession,
  currentRootId,
  onSessionClick,
  onDraggingChange,
  onNewSession,
  onResetForNewSession,
  onSelectProject,
  onSelectSession,
}: {
  hasBoundSession: boolean;
  canOpenSessionDrawer: boolean;
  sessionDrawerOpen: boolean;
  detachedBoundSession: boolean;
  accentColorRaw: string;
  accentHex: string;
  currentSession: SessionInfo | null;
  currentRootId?: string | null;
  onSessionClick: () => void;
  onDraggingChange?: (dragging: boolean) => void;
  onNewSession?: () => void;
  onResetForNewSession?: () => void;
  onSelectProject?: (rootId: string) => void;
  onSelectSession?: (session: Session) => void;
}) {
  const { t } = useI18n();
  const [dragX, setDragX] = useState(0);
  const [dragY, setDragY] = useState(0);
  const [isDragging, setIsDragging] = useState(false);
  const dragStartRef = useRef({ x: 0, y: 0 });
  const [quickSwitchOpen, setQuickSwitchOpen] = useState(false);
  const [quickSwitchLoading, setQuickSwitchLoading] = useState(false);
  const [quickSwitchGroups, setQuickSwitchGroups] = useState<QuickSwitchGroup[]>([]);
  const [quickSwitchPos, setQuickSwitchPos] = useState({ left: 8, bottom: 64, width: 272, maxHeight: 400 });
  const rootRef = useRef<HTMLDivElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);

  const boundRingColor = detachedBoundSession ? "#f59e0b" : accentHex;
  const boundRingShadow = detachedBoundSession
    ? "0 0 0 1px rgba(245,158,11,0.18)"
    : `0 0 0 1px ${appAccentHexToRgba(accentHex, 0.08)}`;
  const boundArrowColor = detachedBoundSession ? "#f59e0b" : accentHex;

  const finishDrag = useCallback(() => {
    if (!isDragging) return;
    // 判定交给 ringGesture：左=-40 新建、上=-40 快捷切换，对角线与回弹方向都判 null。
    const action = ringGesture(dragX, dragY);
    if (action === "new") {
      onResetForNewSession?.();
      onNewSession?.();
    }
    if (action === "switch") setQuickSwitchOpen(true);
    setDragX(0);
    setDragY(0);
    setIsDragging(false);
  }, [isDragging, dragX, dragY, onNewSession, onResetForNewSession]);

  const handleDragStart = (e: React.MouseEvent | React.TouchEvent) => {
    const point = "touches" in e ? e.touches[0] : e;
    dragStartRef.current = { x: point.clientX, y: point.clientY };
    setIsDragging(true);
  };

  useEffect(() => {
    onDraggingChange?.(isDragging);
    return () => onDraggingChange?.(false);
  }, [isDragging, onDraggingChange]);

  useEffect(() => {
    if (!isDragging) return;
    const move = (e: MouseEvent | TouchEvent) => {
      const point = "touches" in e ? e.touches[0] : e;
      setDragX(Math.min(0, point.clientX - dragStartRef.current.x));
      setDragY(Math.min(0, point.clientY - dragStartRef.current.y));
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

  // 快捷切换面板：贴着输入框上沿展开，只取最近 3 个项目 × 每项目 3 条。
  useEffect(() => {
    if (!quickSwitchOpen) return;
    let active = true;
    setQuickSwitchLoading(true);
    void sessionService
      .fetchMultiRootSessions(3)
      .then((items) => {
        if (!active) return;
        setQuickSwitchGroups(recentQuickSwitchGroups(items));
        setQuickSwitchLoading(false);
      })
      .catch(() => {
        if (active) setQuickSwitchLoading(false);
      });
    const place = () => {
      const rect = rootRef.current?.closest('[data-onboarding="message-input"]')?.getBoundingClientRect();
      if (!rect) return;
      const width = Math.min(272, window.innerWidth - 16);
      setQuickSwitchPos({
        left: Math.max(8, Math.min(rect.right - width, window.innerWidth - width - 8)),
        bottom: window.innerHeight - rect.top,
        width,
        maxHeight: Math.max(0, Math.min(440, rect.top - 16)),
      });
    };
    const outside = (event: PointerEvent) => {
      const target = event.target as Node;
      if (!panelRef.current?.contains(target) && !rootRef.current?.contains(target)) {
        setQuickSwitchOpen(false);
      }
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        setQuickSwitchOpen(false);
      }
    };
    place();
    window.addEventListener("resize", place);
    window.addEventListener("scroll", place, true);
    document.addEventListener("pointerdown", outside);
    window.addEventListener("keydown", escape, true);
    return () => {
      active = false;
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
      document.removeEventListener("pointerdown", outside);
      window.removeEventListener("keydown", escape, true);
    };
  }, [quickSwitchOpen]);

  const gesture = ringGesture(dragX, dragY);
  const quickSwitchEnabled = !!onSelectProject || !!onSelectSession;
  const rowStyle: React.CSSProperties = {
    display: "block",
    width: "100%",
    border: 0,
    background: "transparent",
    color: "var(--text-primary)",
    textAlign: "left",
    padding: "4px 6px",
    borderRadius: 6,
    cursor: "pointer",
    overflow: "hidden",
    textOverflow: "ellipsis",
    whiteSpace: "nowrap",
    fontSize: 13,
    lineHeight: "18px",
  };

  return (
    <>
    <div
      ref={rootRef}
      data-onboarding="session-ring"
      onMouseDown={handleDragStart}
      onTouchStart={handleDragStart}
      onClick={() => {
        // 拖完松手的那一下也会触发 click，靠位移阈值把它和点按分开。
        if (Math.abs(dragX) < 5 && Math.abs(dragY) < 5) {
          onSessionClick?.();
        }
      }}
      style={{
        width: "32px",
        height: "32px",
        cursor: "pointer",
        transform: `translate(${dragX}px, ${dragY}px)`,
        transition: isDragging ? "none" : "all 0.3s cubic-bezier(0.4, 0, 0.2, 1)",
        position: "relative",
        zIndex: 10,
        opacity: 1,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        touchAction: "none",
      }}
      title={quickSwitchEnabled ? t("action.ringHint") : t("action.swipeNewSession")}
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
      {isDragging && (dragX < -10 || dragY < -10) ? (
        <div
          style={{
            position: "absolute",
            right: "100%",
            top: "50%",
            transform: "translateY(-50%)",
            marginRight: "8px",
            fontSize: "10px",
            fontWeight: 600,
            color: gesture ? accentColorRaw : "#9ca3af",
            whiteSpace: "nowrap",
            opacity: Math.min(1, Math.max(Math.abs(dragX), Math.abs(dragY)) / 20),
            pointerEvents: "none",
          }}
        >
          {gesture === "new"
            ? t("action.releaseNewSession")
            : gesture === "switch"
              ? t("action.releaseQuickSwitch")
              : t(quickSwitchEnabled ? "action.ringHint" : "action.swipeNewSession")}
        </div>
      ) : null}
    </div>
    {quickSwitchOpen ? createPortal(
      <div
        ref={panelRef}
        role="dialog"
        aria-label={t("action.quickSwitch")}
        tabIndex={-1}
        style={{
          position: "fixed",
          ...quickSwitchPos,
          boxSizing: "border-box",
          zIndex: 1200,
          overflowY: "auto",
          padding: "6px 4px",
          background: "var(--menu-bg)",
          border: "1px solid var(--menu-border)",
          borderTop: 0,
          borderRadius: 12,
          boxShadow: "0 8px 32px rgba(0,0,0,0.18)",
        }}
      >
        {quickSwitchLoading ? (
          <div role="status" style={{ padding: "8px 6px" }}>{t("common.loading")}</div>
        ) : quickSwitchGroups.length === 0 ? (
          <div style={{ padding: "8px 6px", color: "var(--text-secondary)" }}>{t("action.quickSwitchEmpty")}</div>
        ) : quickSwitchGroups.map((group, index) => (
          <div key={group.rootId} style={{ padding: "2px 0", borderTop: index === 0 ? 0 : "1px solid var(--menu-border)" }}>
            {onSelectProject ? (
              <button
                className="quick-switch-row"
                type="button"
                title={group.rootName || group.rootId}
                style={{
                  ...rowStyle,
                  fontWeight: 600,
                  color: group.rootId === currentRootId ? accentColorRaw : "var(--text-primary)",
                }}
                onClick={() => {
                  setQuickSwitchOpen(false);
                  onSelectProject(group.rootId);
                }}
              >
                {group.rootName || group.rootId}
              </button>
            ) : null}
            {group.items.map((session) => (
              <button
                className="quick-switch-row"
                type="button"
                key={session.key || session.session_key}
                title={session.name || session.key || session.session_key}
                aria-current={group.rootId === currentRootId && (session.key || session.session_key) === currentSession?.key ? "true" : undefined}
                style={{ ...rowStyle, paddingLeft: 18, color: "var(--text-secondary)" }}
                onClick={() => {
                  if (!onSelectSession) return;
                  setQuickSwitchOpen(false);
                  onSelectSession({ ...session, root_id: group.rootId } as Session);
                }}
              >
                {session.name || session.key || session.session_key}
              </button>
            ))}
          </div>
        ))}
        <style>{`.quick-switch-row:hover, .quick-switch-row:focus-visible, .quick-switch-row[aria-current="true"] { background: ${appAccentHexToRgba(accentHex, 0.1)} !important; }`}</style>
      </div>,
      document.body,
    ) : null}
    </>
  );
}
