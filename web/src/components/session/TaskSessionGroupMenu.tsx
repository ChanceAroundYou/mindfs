import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { useI18n } from "../../i18n/index";
import type { SessionItem } from "./sessionListTypes";
import { ArchiveIcon } from "./sessionListShared";
import { menuItemStyle } from "./sessionCardHelpers";
import { clipBounds } from "./SessionCardOverflowMenu";
import { archiveTaskGroup, deleteTaskGroup } from "./taskGroupActions";

/**
 * 任务分组头的三点菜单（2026-11-20 用户要求：分组也要有三点操作按钮）。
 *
 * 四条裁定：
 *   1. 重命名、同步**保留但置灰不可点** —— 分组不是一个会话，改名/同步都不知道
 *      该落到哪一条成员上，藏起来又会让人以为功能没了；
 *   2. 归档、删除照常可用，并且**递归覆盖全部成员及其子会话**；
 *   3. 两者都要额外确认，确认框里写明影响多少条会话（见 taskGroupActions.ts）；
 *   4. 归档视图里（成员都已归档）归档项变成「取消归档」；那里没有删除入口，
 *      所以 onDelete 缺席时整条删除项不渲染。
 *
 * 弹层定位与裁剪判据复用会话卡那套（clipBounds + 上下空间实测），分组头同样
 * 住在会话列表那个 overflow:auto 容器里，越过它就被切。
 */
export function TaskSessionGroupMenu({
  name,
  members,
  archived,
  onArchive,
  onDelete,
}: {
  name: string;
  members: SessionItem[];
  archived: boolean;
  onArchive?: (session: SessionItem, archived: boolean) => Promise<boolean> | boolean;
  onDelete?: (session: SessionItem, options?: { confirm?: boolean }) => Promise<void> | void;
}) {
  const { t } = useI18n();
  const triggerRef = useRef<HTMLDivElement | null>(null);
  const boxRef = useRef<HTMLDivElement | null>(null);
  const [open, setOpen] = useState(false);
  const [openUp, setOpenUp] = useState(false);

  useEffect(() => {
    if (!open) return;
    const handlePointerDown = (event: MouseEvent) => {
      if (!triggerRef.current?.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", handlePointerDown);
    return () => document.removeEventListener("mousedown", handlePointerDown);
  }, [open]);

  useLayoutEffect(() => {
    if (!open) {
      setOpenUp(false);
      return;
    }
    const place = () => {
      const trigger = triggerRef.current;
      const box = boxRef.current;
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
  }, [open]);

  const disabledStyle = {
    ...menuItemStyle,
    color: "var(--text-secondary)",
    opacity: 0.45,
    cursor: "default",
  };

  return (
    <div
      ref={triggerRef}
      style={{ position: "relative", flexShrink: 0, marginLeft: "2px" }}
      // 分组头整行是展开/收起开关：菜单里的任何点击都不能冒泡成「切换分组」。
      onClick={(event) => event.stopPropagation()}
    >
      <button
        type="button"
        aria-label={t("sessionList.taskGroupMenu")}
        aria-expanded={open}
        onClick={(event) => {
          event.stopPropagation();
          setOpen((prev) => !prev);
        }}
        style={{
          width: "28px",
          height: "28px",
          borderRadius: "8px",
          border: "none",
          background: open ? "rgba(0, 0, 0, 0.06)" : "transparent",
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
      </button>
      {open ? (
        <div
          ref={boxRef}
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
          {/* 置灰项保留在菜单里（用户 2026-11-20）：能力没消失，只是分组这一层
              没有「改哪一条」的答案，所以 title 里说明原因而不是静默隐藏。 */}
          <button
            type="button"
            disabled
            title={t("sessionList.taskGroupRenameUnsupported")}
            style={disabledStyle}
          >
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M12 20h9" />
              <path d="M16.5 3.5a2.12 2.12 0 1 1 3 3L7 19l-4 1 1-4 12.5-12.5z" />
            </svg>
            {t("sessionList.rename")}
          </button>
          <button
            type="button"
            disabled
            title={t("sessionList.taskGroupSyncUnsupported")}
            style={disabledStyle}
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" aria-hidden="true">
              <path fill="currentColor" d="M19.91 15.51h-4.53a1 1 0 0 0 0 2h2.4A8 8 0 0 1 4 12a1 1 0 0 0-2 0a10 10 0 0 0 16.88 7.23V21a1 1 0 0 0 2 0v-4.5a1 1 0 0 0-.97-.99M12 2a10 10 0 0 0-6.88 2.77V3a1 1 0 0 0-2 0v4.5a1 1 0 0 0 1 1h4.5a1 1 0 0 0 0-2h-2.4A8 8 0 0 1 20 12a1 1 0 0 0 2 0A10 10 0 0 0 12 2" />
            </svg>
            {t("sessionList.sync")}
          </button>
          {onArchive ? (
            <button
              type="button"
              onClick={(event) => {
                event.stopPropagation();
                setOpen(false);
                void archiveTaskGroup({ members, name, archived, t, onArchive });
              }}
              style={{ ...menuItemStyle, color: "var(--text-primary)" }}
            >
              <span style={{ width: "13px", height: "13px", display: "inline-flex" }}>
                <ArchiveIcon />
              </span>
              {archived ? t("sessionList.unarchive") : t("sessionList.archive")}
            </button>
          ) : null}
          {onDelete ? (
            <button
              type="button"
              onClick={(event) => {
                event.stopPropagation();
                setOpen(false);
                void deleteTaskGroup({ members, name, t, onDelete });
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
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
