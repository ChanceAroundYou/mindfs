import React from "react";
import { createPortal } from "react-dom";
import { hexToRgba } from "../action/composerStyles";
import { useSelect, type SelectOption, type SelectProps } from "./useSelect";

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
  const {
    isOpen,
    setIsOpen,
    pos,
    reset,
    highlight,
    setHighlight,
    dropdownRef,
    menuRef,
    selected,
    triggerText,
    selectedColor,
    accentHex,
    pick,
  } = useSelect({
    value,
    options,
    onChange,
    disabled,
    displayLabel,
    placeholder,
    accentColor,
    onOpenChange,
  });

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
            <span style={{
              flex: 1,
              minWidth: 0,
              overflow: "hidden",
              textOverflow: "ellipsis",
              // 逐项着色优先于选中态的 accentHex：项目名要的是各自的节点色，
              // 不是「选中项统一高亮成一种颜色」。
              ...(option.color ? { color: option.color } : null),
            }}>
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
          reset();
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
          color: selectedColor
            || (selected || displayLabel ? "var(--text-color)" : "var(--text-secondary)"),
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
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0, opacity: 0.5 }}>
      <polyline points="6 9 12 15 18 9" />
    </svg>
  );
}
