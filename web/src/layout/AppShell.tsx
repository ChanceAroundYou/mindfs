import React, { useRef, useState, useEffect } from "react";
import { useI18n } from "../i18n";

type AppShellProps = {
  sidebar: React.ReactNode;
  main: React.ReactNode;
  rightSidebar?: React.ReactNode;
  footer: React.ReactNode;
  drawer?: React.ReactNode;
  leftOpen?: boolean;
  rightOpen?: boolean;
  onCloseLeft?: () => void;
  onCloseRight?: () => void;
  onOpenLeft?: () => void;
  onOpenRight?: () => void;
  sidebarsSwapped?: boolean;
};

const MOBILE_BREAKPOINT = 768;
const TABLET_BREAKPOINT = 1024;
const SIDEBAR_MIN_WIDTH = 180;
const SIDEBAR_MAX_WIDTH = 480;

type RailSide = "left" | "right";

type RailDragState = {
  side: RailSide;
  startX: number;
  startWidth: number;
  open: boolean;
  dragged: boolean;
};

function useResponsive() {
  const [isMobile, setIsMobile] = useState(false);
  const [isTablet, setIsTablet] = useState(false);
  useEffect(() => {
    const checkSize = () => {
      const width = window.innerWidth;
      setIsMobile(width < MOBILE_BREAKPOINT);
      setIsTablet(width >= MOBILE_BREAKPOINT && width < TABLET_BREAKPOINT);
    };
    checkSize();
    window.addEventListener("resize", checkSize);
    return () => window.removeEventListener("resize", checkSize);
  }, []);
  return { isMobile, isTablet };
}

const sidebarStyle: React.CSSProperties = {
  gridArea: "sidebar",
  borderRight: "1px solid var(--border-color)",
  overflow: "auto",
  background: "var(--mindfs-topbar-bg, var(--sidebar-bg))",
  display: "flex",
  flexDirection: "column",
  position: "relative",
  zIndex: 10,
  minWidth: 0,
};

const mainStyle: React.CSSProperties = {
  gridArea: "main",
  overflow: "hidden",
  padding: "0",
  background: "var(--mindfs-topbar-bg, var(--mobile-overlay-bg, var(--content-bg)))",
  display: "flex",
  flexDirection: "column",
  minHeight: 0,
  position: "relative",
  zIndex: 1,
  contain: "paint",
};

const rightStyle: React.CSSProperties = {
  gridArea: "right",
  borderLeft: "1px solid var(--border-color)",
  overflow: "auto",
  background: "var(--mindfs-topbar-bg, var(--sidebar-bg))",
  display: "flex",
  flexDirection: "column",
  position: "relative",
  zIndex: 10,
  minWidth: 0,
};

const footerStyle: React.CSSProperties = {
  gridArea: "footer",
  borderTop: "none",
  padding: "0",
  display: "flex",
  alignItems: "flex-end",
  justifyContent: "center",
  background: "var(--mindfs-topbar-bg, var(--mobile-overlay-bg, var(--content-bg)))",
  zIndex: 100,
  minWidth: 0,
};

export function AppShell({
  sidebar,
  main,
  rightSidebar,
  footer,
  drawer,
  leftOpen = true,
  rightOpen = true,
  onCloseLeft,
  onCloseRight,
  onOpenLeft,
  onOpenRight,
  sidebarsSwapped = false,
}: AppShellProps) {
  const { t } = useI18n();
  const { isMobile, isTablet } = useResponsive();

  const [sidebarWidthPx, setSidebarWidthPx] = useState(() =>
    typeof window === "undefined" ? 260 : window.innerWidth < TABLET_BREAKPOINT ? 200 : 260
  );
  const [rightWidthPx, setRightWidthPx] = useState(() =>
    typeof window === "undefined" ? 280 : rightSidebar ? (window.innerWidth < TABLET_BREAKPOINT ? 240 : 280) : 0
  );

  const [isResizing, setIsResizing] = useState(false);
  const railDragRef = useRef<RailDragState | null>(null);
  const resizePendingRef = useRef<{ side: RailSide; width: number } | null>(null);
  const resizeRafRef = useRef(0);

  useEffect(
    () => () => {
      if (resizeRafRef.current) cancelAnimationFrame(resizeRafRef.current);
    },
    []
  );

  const getRailWidth = (side: RailSide): number => {
    if (side === "left") return sidebarsSwapped ? rightWidthPx : sidebarWidthPx;
    return sidebarsSwapped ? sidebarWidthPx : rightWidthPx;
  };

  const setRailWidth = (side: RailSide, width: number) => {
    if (side === "left") {
      if (sidebarsSwapped) setRightWidthPx(width);
      else setSidebarWidthPx(width);
    } else if (sidebarsSwapped) {
      setSidebarWidthPx(width);
    } else {
      setRightWidthPx(width);
    }
  };

  const toggleRail = (side: RailSide) => {
    if (side === "left") (physicalLeftOpen ? physicalLeftClose : physicalLeftOpenHandler)?.();
    else (physicalRightOpen ? physicalRightClose : physicalRightOpenHandler)?.();
  };

  const handleRailPointerDown = (side: RailSide) => (e: React.PointerEvent<HTMLButtonElement>) => {
    if (e.pointerType === "mouse" && e.button !== 0) return;
    try {
      e.currentTarget.setPointerCapture(e.pointerId);
    } catch {
      /* pointer already released */
    }
    railDragRef.current = {
      side,
      startX: e.clientX,
      startWidth: getRailWidth(side),
      open: side === "left" ? physicalLeftOpen : physicalRightOpen,
      dragged: false,
    };
  };

  const handleRailPointerMove = (e: React.PointerEvent<HTMLButtonElement>) => {
    const st = railDragRef.current;
    if (!st?.open) return;
    const dx = e.clientX - st.startX;
    if (!st.dragged) {
      if (Math.abs(dx) < 5) return;
      st.dragged = true;
      setIsResizing(true);
    }
    const raw = st.startWidth + (st.side === "left" ? dx : -dx);
    const next = Math.min(SIDEBAR_MAX_WIDTH, Math.max(SIDEBAR_MIN_WIDTH, raw));
    resizePendingRef.current = { side: st.side, width: next };
    if (!resizeRafRef.current) {
      resizeRafRef.current = requestAnimationFrame(() => {
        resizeRafRef.current = 0;
        const pending = resizePendingRef.current;
        resizePendingRef.current = null;
        if (pending) setRailWidth(pending.side, pending.width);
      });
    }
  };

  const handleRailPointerEnd = (e: React.PointerEvent<HTMLButtonElement>, allowToggle: boolean) => {
    const st = railDragRef.current;
    railDragRef.current = null;
    if (!st) return;
    if (resizeRafRef.current) {
      cancelAnimationFrame(resizeRafRef.current);
      resizeRafRef.current = 0;
    }
    const pending = resizePendingRef.current;
    resizePendingRef.current = null;
    try {
      e.currentTarget.releasePointerCapture(e.pointerId);
    } catch {
      /* capture may already be released */
    }
    if (st.dragged) {
      if (pending) setRailWidth(pending.side, pending.width);
      setIsResizing(false);
    } else if (allowToggle) {
      toggleRail(st.side);
    }
  };

  const handleRailKeyDown = (side: RailSide) => (e: React.KeyboardEvent<HTMLButtonElement>) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      toggleRail(side);
    }
  };

  const sidebarWidth = isMobile ? "0px" : `${sidebarWidthPx}px`;
  const rightWidth = isMobile ? "0px" : (rightSidebar ? `${rightWidthPx}px` : "0px");
  const mobileHeight = "var(--mindfs-viewport-height, 100dvh)";
  const physicalLeftOpen = sidebarsSwapped ? rightOpen : leftOpen;
  const physicalRightOpen = sidebarsSwapped ? leftOpen : rightOpen;
  const physicalLeftWidth = sidebarsSwapped ? rightWidth : sidebarWidth;
  const physicalRightWidth = sidebarsSwapped ? sidebarWidth : rightWidth;
  const physicalLeftContent = sidebarsSwapped ? rightSidebar : sidebar;
  const physicalRightContent = sidebarsSwapped ? sidebar : rightSidebar;
  const physicalLeftClose = sidebarsSwapped ? onCloseRight : onCloseLeft;
  const physicalLeftOpenHandler = sidebarsSwapped ? onOpenRight : onOpenLeft;
  const physicalRightClose = sidebarsSwapped ? onCloseLeft : onCloseRight;
  const physicalRightOpenHandler = sidebarsSwapped ? onOpenLeft : onOpenRight;
  const physicalLeftLabel = sidebarsSwapped ? t("sidebar.session") : t("sidebar.file");
  const physicalRightLabel = sidebarsSwapped ? t("sidebar.file") : t("sidebar.session");

  const shellStyle: React.CSSProperties & {
    "--mindfs-actionbar-bottom-padding"?: string;
    "--mindfs-file-menu-width"?: string;
  } = {
    display: isMobile ? "flex" : "grid",
    flexDirection: isMobile ? "column" : undefined,
    gridTemplateColumns: isMobile ? undefined : `${physicalLeftOpen ? physicalLeftWidth : "0px"} 1fr ${physicalRightOpen ? physicalRightWidth : "0px"}`,
    gridTemplateRows: isMobile ? undefined : "1fr auto",
    gridTemplateAreas: isMobile ? undefined : `"sidebar main right" "sidebar footer right"`,
    minHeight: isMobile ? mobileHeight : "100vh",
    height: isMobile ? mobileHeight : "100dvh",
    background: isMobile
      ? "var(--mindfs-topbar-bg, var(--mindfs-system-bar-bg, var(--mobile-overlay-bg, var(--content-bg))))"
      : "var(--bg-gradient-composite, var(--bg-gradient-start, #f3f4f6))",
    color: "var(--text-primary)",
    position: "relative",
    width: isMobile ? "100%" : undefined,
    maxWidth: isMobile ? "100%" : undefined,
    paddingTop: isMobile ? "var(--mindfs-safe-area-top, env(safe-area-inset-top, 0px))" : undefined,
    overflow: "hidden",
    isolation: "isolate",
    boxSizing: "border-box",
    transition: isResizing
      ? "none"
      : "grid-template-columns 0.3s cubic-bezier(0.4, 0, 0.2, 1)",
    willChange: isResizing ? "grid-template-columns" : undefined,
    userSelect: isResizing ? "none" : undefined,
    "--mindfs-actionbar-bottom-padding": "calc(var(--mindfs-safe-area-bottom) + 12px)",
    "--mindfs-file-menu-width": isMobile ? "52.5vw" : (isTablet ? "140px" : "182px"),
  };

  const mobileSidebarStyle = (side: 'left' | 'right'): React.CSSProperties => ({
    position: "fixed",
    top: "var(--mindfs-safe-area-top, env(safe-area-inset-top, 0px))",
    bottom: 0,
    [side]: 0,
    width: "75vw",
    zIndex: 2000,
    background: "var(--mindfs-topbar-bg, var(--mobile-sidebar-bg, var(--sidebar-bg)))",
    boxShadow: side === 'left' ? "4px 0 24px rgba(0,0,0,0.15)" : "-4px 0 24px rgba(0,0,0,0.15)",
    transition: "transform 0.22s cubic-bezier(0.2, 0.8, 0.2, 1)",
    display: "flex",
    flexDirection: "column",
    overflow: "hidden",
    borderTopRightRadius: side === 'left' ? "14px" : undefined,
    borderBottomRightRadius: side === 'left' ? "14px" : undefined,
    borderTopLeftRadius: side === 'right' ? "14px" : undefined,
    borderBottomLeftRadius: side === 'right' ? "14px" : undefined,
    willChange: "transform",
    backfaceVisibility: "hidden",
    transform: "translateX(0) translateZ(0)",
  });

  const overlayStyle: React.CSSProperties = {
    position: "fixed",
    inset: 0,
    background: "rgba(0,0,0,0.3)",
    zIndex: 1500,
    opacity: (isMobile && (leftOpen || rightOpen)) ? 1 : 0,
    pointerEvents: (isMobile && (leftOpen || rightOpen)) ? "auto" : "none",
    transition: "opacity 0.18s ease",
    willChange: "opacity",
    backfaceVisibility: "hidden",
    transform: "translateZ(0)",
  };

  const mobileFooterStyle: React.CSSProperties = {
    ...footerStyle,
    flexShrink: 0,
  };

  return (
    <div style={shellStyle} data-onboarding="shell">
      {isMobile && <div style={overlayStyle} onClick={() => { onCloseLeft?.(); onCloseRight?.(); }} />}

      {(!isMobile || physicalLeftOpen) && physicalLeftContent ? (
        <aside
          style={
            isMobile
              ? mobileSidebarStyle('left')
              : {
                  ...sidebarStyle,
                  overflow: physicalLeftOpen ? "auto" : "hidden",
                  pointerEvents: physicalLeftOpen ? "auto" : "none",
                }
          }
        >
          {physicalLeftContent}
        </aside>
      ) : null}

      <main
        style={
          isMobile
            ? {
                ...mainStyle,
                flex: 1,
                minHeight: 0,
                minWidth: 0,
              }
            : mainStyle
        }
      >
        {main}
        {/* 将抽屉层放入主视图内部，确保绝对定位时能精准对齐主视图宽度 */}
        {drawer}
      </main>

      {(!isMobile || physicalRightOpen) && physicalRightContent ? (
        <aside
          style={
            isMobile
              ? mobileSidebarStyle('right')
              : {
                  ...rightStyle,
                  overflow: physicalRightOpen ? "auto" : "hidden",
                  pointerEvents: physicalRightOpen ? "auto" : "none",
                }
          }
        >
          {physicalRightContent}
        </aside>
      ) : null}

      {!isMobile ? (
        <>
          <button
            type="button"
            className={`mindfs-sidebar-resize-rail mindfs-sidebar-resize-rail--left${physicalLeftOpen ? " is-open" : " is-closed"}`}
            onPointerDown={handleRailPointerDown("left")}
            onPointerMove={handleRailPointerMove}
            onPointerUp={(e) => handleRailPointerEnd(e, true)}
            onPointerCancel={(e) => handleRailPointerEnd(e, false)}
            onKeyDown={handleRailKeyDown("left")}
            aria-label={physicalLeftOpen ? t("sidebar.collapse", { label: physicalLeftLabel }) : t("sidebar.expand", { label: physicalLeftLabel })}
            title={physicalLeftOpen ? t("sidebar.collapse", { label: physicalLeftLabel }) : t("sidebar.expand", { label: physicalLeftLabel })}
            style={{
              left: physicalLeftOpen ? `calc(${physicalLeftWidth} - 6px)` : 0,
              cursor: physicalLeftOpen ? "col-resize" : "e-resize",
            }}
          />
          {physicalRightContent ? (
            <button
              type="button"
              className={`mindfs-sidebar-resize-rail mindfs-sidebar-resize-rail--right${physicalRightOpen ? " is-open" : " is-closed"}`}
              onPointerDown={handleRailPointerDown("right")}
              onPointerMove={handleRailPointerMove}
              onPointerUp={(e) => handleRailPointerEnd(e, true)}
              onPointerCancel={(e) => handleRailPointerEnd(e, false)}
              onKeyDown={handleRailKeyDown("right")}
              aria-label={physicalRightOpen ? t("sidebar.collapse", { label: physicalRightLabel }) : t("sidebar.expand", { label: physicalRightLabel })}
              title={physicalRightOpen ? t("sidebar.collapse", { label: physicalRightLabel }) : t("sidebar.expand", { label: physicalRightLabel })}
              style={{
                right: physicalRightOpen ? `calc(${physicalRightWidth} - 6px)` : 0,
                cursor: physicalRightOpen ? "col-resize" : "w-resize",
              }}
            />
          ) : null}
        </>
      ) : null}

      <footer
        style={
          isMobile
            ? mobileFooterStyle
            : footerStyle
        }
      >
        {footer}
      </footer>
    </div>
  );
}
