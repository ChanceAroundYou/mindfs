import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { hexToRgba } from "./action/composerStyles";

export type SelectOption<T extends string = string> = {
  value: T;
  label: string;
  disabled?: boolean;
  /** 右侧次要说明文字 */
  hint?: string;
};

export type SelectProps<T extends string = string> = {
  value: T;
  options: SelectOption<T>[];
  onChange: (value: T) => void;
  disabled?: boolean;
  /** 收起态显示的文字；不传则取选中项 label。用于「跟随模板」这类占位语义。 */
  displayLabel?: string;
  /** value 为空字符串时收起态显示的字（次要色） */
  placeholder?: string;
  accentColor?: string | null;
  /** panel=26px（选项条 / 弹窗头部），chip 走默认 */
  size?: "panel" | "chip";
  ariaLabel?: string;
  maxWidth?: number;
  onOpenChange?: (open: boolean) => void;
};

const MENU_MARGIN = 8;

/**
 * 应用内统一下拉。
 *
 * 原生 `<select>` 的收起态能靠 CSS 改，但点开后的 option 列表是操作系统菜单，
 * 深浅色都统一不了、样式也各系统不同 —— 所以这里自绘：触发按钮 + portal 菜单。
 *
 * 一律 portal 到 body 并用 position: fixed 定位：三张任务面板的 body 都是
 * overflow:auto，内联/绝对定位的菜单会被裁掉。定位沿用 ModeSelector / AgentSelector
 * 已验证的算法（放下方，放不下翻到上方，并夹在视口内）。
 */
export function Select<T extends string = string>({
  value,
  options,
  onChange,
  disabled,
  displayLabel,
  placeholder,
  accentColor,
  size = "chip",
  ariaLabel,
  maxWidth,
  onOpenChange,
}: SelectProps<T>) {
  const accentHex = String(accentColor || "").trim() || "#3b82f6";
  const [isOpen, setIsOpen] = useState(false);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);
  const [positionTick, setPositionTick] = useState(0);
  const [highlight, setHighlight] = useState(-1);
  const dropdownRef = useRef<HTMLDivElement | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);

  const selected = useMemo(() => options.find((item) => item.value === value) || null, [options, value]);
  const triggerText = displayLabel ?? selected?.label ?? placeholder ?? "";

  // 视口变化时重算（软键盘弹出 / 窗口缩放会改变可用高度）
  useEffect(() => {
    if (!isOpen) return;
    const recompute = () => setPositionTick((tick) => tick + 1);
    window.visualViewport?.addEventListener("resize", recompute);
    window.addEventListener("resize", recompute);
    window.addEventListener("scroll", recompute, true);
    return () => {
      window.visualViewport?.removeEventListener("resize", recompute);
      window.removeEventListener("resize", recompute);
      window.removeEventListener("scroll", recompute, true);
    };
  }, [isOpen]);

  useEffect(() => {
    if (disabled) setIsOpen(false);
  }, [disabled]);

  useEffect(() => {
    onOpenChange?.(isOpen);
  }, [isOpen, onOpenChange]);

  useEffect(() => {
    if (!isOpen) return;
    const onPointerOutside = (event: PointerEvent) => {
      const target = event.target as Node;
      if (!dropdownRef.current?.contains(target) && !menuRef.current?.contains(target)) {
        setIsOpen(false);
      }
    };
    document.addEventListener("pointerdown", onPointerOutside);
    return () => document.removeEventListener("pointerdown", onPointerOutside);
  }, [isOpen]);

  // 打开时把高亮落在当前选中项上
  useEffect(() => {
    if (!isOpen) return;
    setHighlight(options.findIndex((item) => item.value === value));
  }, [isOpen, options, value]);

  useLayoutEffect(() => {
    if (!isOpen || !dropdownRef.current || !menuRef.current) return;
    const anchor = dropdownRef.current.getBoundingClientRect();
    const menu = menuRef.current.getBoundingClientRect();
    const viewport = window.visualViewport;
    const viewportLeft = viewport?.offsetLeft ?? 0;
    const viewportTop = viewport?.offsetTop ?? 0;
    const viewportWidth = viewport?.width ?? window.innerWidth;
    const viewportHeight = viewport?.height ?? window.innerHeight;

    // 右边缘对齐触发按钮右边缘，再夹进视口
    const left = Math.max(
      viewportLeft + MENU_MARGIN,
      Math.min(anchor.right - menu.width, viewportLeft + viewportWidth - menu.width - MENU_MARGIN),
    );
    // 默认放下方；下方放不下就翻到上方
    const below = anchor.bottom + MENU_MARGIN;
    const above = anchor.top - menu.height - MENU_MARGIN;
    const fitsBelow = below + menu.height <= viewportTop + viewportHeight - MENU_MARGIN;
    const top = fitsBelow ? below : Math.max(viewportTop + MENU_MARGIN, above);
    setPos((current) => (
      current && Math.abs(current.top - top) < 0.5 && Math.abs(current.left - left) < 0.5
        ? current
        : { top, left }
    ));
  }, [isOpen, positionTick, options.length, value]);

  const pick = useCallback((option: SelectOption<T>) => {
    if (option.disabled) return;
    onChange(option.value);
    setIsOpen(false);
  }, [onChange]);

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (disabled) return;
    if (event.key === "Escape") {
      setIsOpen(false);
      return;
    }
    if (event.key === "Enter" || event.key === " ") {
      if (isOpen && highlight >= 0 && options[highlight]) {
        event.preventDefault();
        pick(options[highlight]);
      } else {
        event.preventDefault();
        setIsOpen(true);
      }
      return;
    }
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (!isOpen) {
        setIsOpen(true);
        return;
      }
      setHighlight((current) => {
        if (options.length === 0) return -1;
        const step = event.key === "ArrowDown" ? 1 : -1;
        const next = current + step;
        if (next < 0) return options.length - 1;
        if (next >= options.length) return 0;
        return next;
      });
    }
  };

  const renderMenu = () => (
    <div
      ref={menuRef}
      role="listbox"
      style={{
        position: "fixed",
        top: pos?.top ?? 0,
        left: pos?.left ?? 0,
        visibility: pos ? "visible" : "hidden",
        background: "var(--menu-bg)",
        border: "1px solid var(--menu-border)",
        borderRadius: "12px",
        boxShadow: "0 8px 32px rgba(0,0,0,0.15)",
        zIndex: 10100,
        minWidth: Math.max(120, dropdownRef.current?.offsetWidth || 0),
        maxWidth: "calc(100vw - 16px)",
        boxSizing: "border-box",
        padding: "4px 0",
        maxHeight: "min(320px, calc(100vh - 32px))",
        overflowY: "auto",
      }}
    >
      {options.map((option, index) => {
        const isSelected = option.value === value;
        return (
          <button
            key={option.value}
            type="button"
            role="option"
            aria-selected={isSelected}
            disabled={option.disabled}
            onMouseDown={(event) => event.preventDefault()}
            onMouseEnter={() => setHighlight(index)}
            onClick={() => pick(option)}
            style={{
              display: "flex",
              alignItems: "center",
              gap: "8px",
              width: "100%",
              padding: size === "panel" ? "7px 12px" : "9px 12px",
              border: "none",
              background: index === highlight
                ? hexToRgba(accentHex, 0.08)
                : isSelected
                  ? hexToRgba(accentHex, 0.05)
                  : "transparent",
              color: isSelected ? accentHex : "var(--text-primary)",
              fontSize: "12px",
              fontWeight: isSelected ? 500 : 400,
              textAlign: "left",
              cursor: option.disabled ? "not-allowed" : "pointer",
              opacity: option.disabled ? 0.45 : 1,
              whiteSpace: "nowrap",
            }}
          >
            <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis" }}>
              {option.label}
            </span>
            {option.hint ? (
              <span style={{ color: "var(--text-secondary)", fontSize: "11px", flexShrink: 0 }}>
                {option.hint}
              </span>
            ) : null}
          </button>
        );
      })}
    </div>
  );

  const height = size === "panel" ? 26 : 30;

  return (
    <div ref={dropdownRef} style={{ position: "relative", minWidth: 0, flexShrink: 0 }}>
      <button
        type="button"
        disabled={disabled}
        aria-label={ariaLabel}
        aria-haspopup="listbox"
        aria-expanded={isOpen}
        onMouseDown={(event) => event.preventDefault()}
        onClick={() => {
          if (disabled) return;
          setPos(null);
          setIsOpen((open) => !open);
        }}
        onKeyDown={onKeyDown}
        style={{
          display: "flex",
          alignItems: "center",
          gap: "6px",
          width: "100%",
          maxWidth,
          height,
          padding: size === "panel" ? "0 6px 0 9px" : "0 8px 0 10px",
          boxSizing: "border-box",
          border: "1px solid var(--border-color)",
          borderRadius: "6px",
          background: "var(--input-bg)",
          color: selected || displayLabel ? "var(--text-color)" : "var(--text-secondary)",
          fontSize: size === "panel" ? "12px" : "12px",
          fontWeight: size === "panel" ? 700 : 500,
          cursor: disabled ? "not-allowed" : "pointer",
          opacity: disabled ? 0.55 : 1,
          outline: "none",
          transition: "border-color 0.15s",
        }}
      >
        <span style={{ flex: 1, minWidth: 0, textAlign: "left", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {triggerText}
        </span>
        <ChevronDown />
      </button>
      {isOpen && !disabled && typeof document !== "undefined"
        ? createPortal(renderMenu(), document.body)
        : null}
    </div>
  );
}

function ChevronDown() {
  return (
    <svg
      width="10"
      height="6"
      viewBox="0 0 10 6"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      style={{ flexShrink: 0, opacity: 0.7 }}
    >
      <polyline points="1 1 5 5 9 1" />
    </svg>
  );
}
