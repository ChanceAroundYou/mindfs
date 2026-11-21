import React, { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../../i18n/index";
import { menuItemStyle } from "../../shared/styles";

export type TaskDetailAction = {
  key: string;
  label: string;
  icon: React.ReactNode;
  /** 破坏性/恢复类动作单独标色，和原来的图标色一致。 */
  tone?: "default" | "primary" | "warn";
  onSelect: () => void;
};

export type TaskDetailActionsMenuProps = {
  actions: TaskDetailAction[];
  /** 保存中：整组动作一起置灰，避免连点。 */
  disabled?: boolean;
};

const toneColor = (tone: TaskDetailAction["tone"]): string =>
  tone === "primary" ? "var(--status-ok)" : tone === "warn" ? "var(--status-warn)" : "var(--text-color)";

/**
 * 任务详情 header 的「操作控件」菜单：收尾 worktree / 完成任务 / 重新打开。
 *
 * 删除**不在**这里：它是终态任务的破坏性动作，仍然单独摆在 header 上（用户明确要求）。
 *
 * 面板卡是 `overflow: hidden`，absolute 弹层会被裁掉，所以 portal 到 body、
 * position: fixed 按触发键的矩形定位（同 `Select` 与连招候选菜单的做法）。
 * 下方放不下就翻到上方；点外面 / Esc / 滚动 / 改窗口大小都收起。
 */
export function TaskDetailActionsMenu({ actions, disabled = false }: TaskDetailActionsMenuProps) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState<{ top: number; right: number } | null>(null);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);

  useLayoutEffect(() => {
    if (!open) {
      setPos(null);
      return;
    }
    const place = () => {
      const trigger = triggerRef.current;
      if (!trigger) return;
      const rect = trigger.getBoundingClientRect();
      const height = menuRef.current?.offsetHeight ?? 0;
      // 视口留 8px 边距；下方不够且上方更宽裕就朝上开。
      const below = window.innerHeight - rect.bottom - 8;
      const above = rect.top - 8;
      const openUp = height > 0 && below < height + 6 && above > below;
      setPos({
        top: openUp ? Math.max(8, rect.top - height - 6) : rect.bottom + 6,
        right: Math.max(8, window.innerWidth - rect.right),
      });
    };
    place();
    // 首帧菜单刚挂载，高度要等布局后才量得到；再对齐一次。
    const raf = requestAnimationFrame(place);
    const onPointerDown = (event: PointerEvent) => {
      const target = event.target as Node | null;
      if (!target) return;
      if (triggerRef.current?.contains(target) || menuRef.current?.contains(target)) return;
      setOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    const onDismiss = () => setOpen(false);
    document.addEventListener("pointerdown", onPointerDown, true);
    document.addEventListener("keydown", onKeyDown, true);
    window.addEventListener("resize", onDismiss);
    window.addEventListener("scroll", onDismiss, true);
    return () => {
      cancelAnimationFrame(raf);
      document.removeEventListener("pointerdown", onPointerDown, true);
      document.removeEventListener("keydown", onKeyDown, true);
      window.removeEventListener("resize", onDismiss);
      window.removeEventListener("scroll", onDismiss, true);
    };
  }, [open]);

  // 动作条件变了（比如任务跑完、canCompleteTask 翻转）就把菜单收起，不留一张过期清单。
  const actionKeys = actions.map((action) => action.key).join("|");
  useEffect(() => {
    setOpen(false);
  }, [actionKeys]);

  const empty = actions.length === 0;

  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        aria-label={t("task.actionsMenu")}
        title={t("task.actionsMenu")}
        aria-haspopup="menu"
        aria-expanded={open}
        disabled={disabled}
        onClick={() => setOpen((value) => !value)}
        style={{
          width: "28px",
          height: "28px",
          flexShrink: 0,
          padding: 0,
          border: "none",
          borderRadius: "8px",
          background: open ? "rgba(0, 0, 0, 0.06)" : "transparent",
          color: "var(--text-secondary)",
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          cursor: disabled ? "not-allowed" : "pointer",
          opacity: disabled ? 0.4 : 1,
        }}
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <circle cx="12" cy="5" r="1.8" />
          <circle cx="12" cy="12" r="1.8" />
          <circle cx="12" cy="19" r="1.8" />
        </svg>
      </button>
      {open && pos
        ? createPortal(
            <div
              ref={menuRef}
              role="menu"
              aria-label={t("task.actionsMenu")}
              style={{
                position: "fixed",
                top: `${pos.top}px`,
                right: `${pos.right}px`,
                minWidth: "168px",
                padding: "6px",
                borderRadius: "10px",
                border: "1px solid var(--border-color)",
                background: "var(--menu-bg)",
                boxShadow: "0 12px 30px rgba(15, 23, 42, 0.14)",
                zIndex: 120,
              }}
            >
              {empty ? (
                <div style={{ ...menuItemStyle, color: "var(--text-secondary)", cursor: "default" }}>
                  {t("task.actionsEmpty")}
                </div>
              ) : (
                actions.map((action) => (
                  <button
                    key={action.key}
                    type="button"
                    role="menuitem"
                    disabled={disabled}
                    onClick={() => {
                      setOpen(false);
                      action.onSelect();
                    }}
                    style={{ ...menuItemStyle, color: toneColor(action.tone), opacity: disabled ? 0.4 : 1 }}
                  >
                    {action.icon}
                    <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                      {action.label}
                    </span>
                  </button>
                ))
              )}
            </div>,
            document.body,
          )
        : null}
    </>
  );
}
