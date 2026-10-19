import { useLayoutEffect, useRef, useState, type RefObject } from "react";
import { useI18n } from "../../i18n/index";
import type { SessionItem } from "./sessionListTypes";
import { ArchiveIcon, PinIcon } from "./sessionListShared";
import { menuItemStyle } from "./sessionCardHelpers";

export type SessionCardOverflowMenuProps = {
  session: SessionItem;
  menuRef: RefObject<HTMLDivElement | null>;
  menuOpen: boolean;
  isPinned: boolean;
  isArchived: boolean;
  selected: boolean;
  syncing: boolean;
  storedName: string;
  onToggle: () => void;
  onClose: () => void;
  onPin?: (session: SessionItem, pinned: boolean) => Promise<boolean> | boolean;
  onSync?: (session: SessionItem) => Promise<void> | void;
  onRename: (name: string) => void;
  onArchive?: (session: SessionItem, archived: boolean) => Promise<boolean> | boolean;
  onDelete?: (session: SessionItem) => void;
};

/**
 * 弹层的可用上下空间要按**裁剪祖先**量，不是按视口。
 *
 * 卡片住在会话列表那个 `flex:1; overflow:auto` 的滚动容器里（SessionList.tsx），
 * `overflow` 非 visible 的盒子按 padding box 裁剪 —— 弹层是 absolute 定位在卡片内的，
 * 越过它就没了。拿 `window.innerHeight` 量会**高估下方空间**（列表下方还有搜索框/按钮时
 * 尤其明显），于是该翻上去的时候判成「放得下」，弹层照样被切。
 */
function clipBounds(el: HTMLElement): { top: number; bottom: number } {
  for (let node = el.parentElement; node; node = node.parentElement) {
    if (getComputedStyle(node).overflowY !== "visible") {
      const rect = node.getBoundingClientRect();
      return { top: rect.top, bottom: rect.bottom };
    }
  }
  return { top: 0, bottom: window.innerHeight };
}

export function SessionCardOverflowMenu({
  session,
  menuRef,
  menuOpen,
  isPinned,
  isArchived,
  selected,
  syncing,
  storedName,
  onToggle,
  onClose,
  onPin,
  onSync,
  onRename,
  onArchive,
  onDelete,
}: SessionCardOverflowMenuProps) {
  const { t } = useI18n();
  // 会话列表最后几行点「⋯」时下方放不下，弹层再往下就撑出视口 —— 那就朝上开。
  // 判据与 ProviderModelSelect 同口径（下方不够 + 上方更宽裕），但这里保持 absolute
  // 定位：弹层跟着行滚动，不需要 portal 那套 fixed 坐标重算。
  const menuBoxRef = useRef<HTMLDivElement | null>(null);
  const [openUp, setOpenUp] = useState(false);

  useLayoutEffect(() => {
    if (!menuOpen) {
      setOpenUp(false);
      return;
    }
    const place = () => {
      const trigger = menuRef.current;
      const box = menuBoxRef.current;
      if (!trigger || !box) return;
      const rect = trigger.getBoundingClientRect();
      const bounds = clipBounds(trigger);
      const below = bounds.bottom - rect.bottom - 6;
      const above = rect.top - bounds.top - 6;
      setOpenUp(below < box.offsetHeight && above > below);
    };
    place();
    window.addEventListener("resize", place);
    window.addEventListener("scroll", place, true);
    return () => {
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
    };
  }, [menuOpen, menuRef]);

  return (
    <div ref={menuRef} style={{ position: "relative", flexShrink: 0, marginLeft: "2px" }}>
      <button
        type="button"
        aria-label={t("sessionList.menu")}
        onClick={(event) => {
          event.stopPropagation();
          onToggle();
        }}
        style={{
          width: "28px",
          height: "28px",
          borderRadius: "8px",
          border: "none",
          background: menuOpen ? "rgba(0, 0, 0, 0.06)" : "transparent",
          color: "var(--text-secondary)",
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          cursor: "pointer",
          outline: "none",
          position: "relative",
        }}
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <circle cx="12" cy="5" r="1.8" />
          <circle cx="12" cy="12" r="1.8" />
          <circle cx="12" cy="19" r="1.8" />
        </svg>
        {isPinned ? (
          <span
            aria-hidden="true"
            style={{
              position: "absolute",
              top: "1px",
              right: "1px",
              width: "12px",
              height: "12px",
              borderRadius: "999px",
              background: "var(--menu-bg)",
              color: selected ? "var(--accent-color)" : "var(--text-secondary)",
              display: "inline-flex",
              alignItems: "center",
              justifyContent: "center",
              pointerEvents: "none",
              boxShadow: "0 0 0 1px var(--content-bg, #fff)",
            }}
          >
            <span style={{ width: "9px", height: "9px", display: "inline-flex" }}>
              <PinIcon pinned />
            </span>
          </span>
        ) : null}
      </button>
      {menuOpen ? (
        <div
          ref={menuBoxRef}
          style={{
            position: "absolute",
            ...(openUp ? { bottom: "calc(100% + 6px)" } : { top: "calc(100% + 6px)" }),
            right: 0,
            minWidth: "120px",
            padding: "6px",
            borderRadius: "10px",
            border: "1px solid var(--border-color)",
            background: "var(--menu-bg)",
            boxShadow: "0 12px 30px rgba(15, 23, 42, 0.14)",
            zIndex: 20,
          }}
        >
          <button
            type="button"
            onClick={(event) => {
              event.stopPropagation();
              onClose();
              void onPin?.(session, !isPinned);
            }}
            style={{ ...menuItemStyle, color: "var(--text-primary)" }}
          >
            <span style={{ width: "13px", height: "13px", display: "inline-flex" }}>
              <PinIcon pinned={isPinned} />
            </span>
            {isPinned ? t("sessionList.unpin") : t("sessionList.pin")}
          </button>
          <button
            type="button"
            disabled={syncing}
            onClick={(event) => {
              event.stopPropagation();
              onClose();
              void onSync?.(session);
            }}
            style={{
              ...menuItemStyle,
              color: "var(--text-primary)",
              opacity: syncing ? 0.55 : 1,
              cursor: syncing ? "default" : "pointer",
            }}
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" aria-hidden="true">
              <path fill="currentColor" d="M19.91 15.51h-4.53a1 1 0 0 0 0 2h2.4A8 8 0 0 1 4 12a1 1 0 0 0-2 0a10 10 0 0 0 16.88 7.23V21a1 1 0 0 0 2 0v-4.5a1 1 0 0 0-.97-.99M12 2a10 10 0 0 0-6.88 2.77V3a1 1 0 0 0-2 0v4.5a1 1 0 0 0 1 1h4.5a1 1 0 0 0 0-2h-2.4A8 8 0 0 1 20 12a1 1 0 0 0 2 0A10 10 0 0 0 12 2" />
            </svg>
            {t("sessionList.sync")}
          </button>
          <button
            type="button"
            onClick={(event) => {
              event.stopPropagation();
              onClose();
              onRename(storedName);
            }}
            style={{ ...menuItemStyle, color: "var(--text-primary)" }}
          >
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M12 20h9" />
              <path d="M16.5 3.5a2.12 2.12 0 1 1 3 3L7 19l-4 1 1-4 12.5-12.5z" />
            </svg>
            {t("sessionList.rename")}
          </button>
          {onArchive ? (
            <button
              type="button"
              onClick={(event) => {
                event.stopPropagation();
                onClose();
                void onArchive(session, !isArchived);
              }}
              style={{ ...menuItemStyle, color: "var(--text-primary)" }}
            >
              <span style={{ width: "13px", height: "13px", display: "inline-flex" }}>
                <ArchiveIcon />
              </span>
              {isArchived ? t("sessionList.unarchive") : t("sessionList.archive")}
            </button>
          ) : null}
          <button
            type="button"
            onClick={(event) => {
              event.stopPropagation();
              onClose();
              onDelete?.(session);
            }}
            style={{ ...menuItemStyle, color: "#dc2626" }}
          >
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <polyline points="3 6 5 6 21 6" />
              <path d="M19 6l-1 14H6L5 6" />
              <path d="M10 11v6" />
              <path d="M14 11v6" />
              <path d="M9 6V4h6v2" />
            </svg>
            {t("common.delete")}
          </button>
        </div>
      ) : null}
    </div>
  );
}
