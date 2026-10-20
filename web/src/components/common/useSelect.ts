import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useAnchoredMenuPosition } from "../../hooks/useAnchoredMenuPosition";

export type SelectOption<T extends string = string> = {
  value: T;
  label: string;
  disabled?: boolean;
  /** 右侧次要说明文字 */
  hint?: string;
  /** 标签字色。用于「项目名用节点色」这种逐项着色的场景；不传则跟默认文字色。 */
  color?: string;
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

export function useSelect<T extends string = string>({
  value,
  options,
  onChange,
  disabled,
  displayLabel,
  placeholder,
  accentColor,
  onOpenChange,
}: SelectProps<T>) {
  const accentHex = String(accentColor || "").trim() || "#3b82f6";
  const [isOpen, setIsOpen] = useState(false);
  const [highlight, setHighlight] = useState(-1);
  const dropdownRef = useRef<HTMLDivElement | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);

  const selected = useMemo(() => options.find((item) => item.value === value) || null, [options, value]);
  const triggerText = displayLabel ?? selected?.label ?? placeholder ?? "";
  // 收起态跟展开的选项列表保持同一套字色，否则列表一关颜色就没了。
  const selectedColor = String(selected?.color || "").trim();

  // 视口变化时重算（软键盘弹出 / 窗口缩放会改变可用高度）——见 useAnchoredMenuPosition。

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

  // 菜单位置（放下方 / 放不下翻上方 / 夹进视口）由共享 hook 统一算。
  const { position: pos, reset } = useAnchoredMenuPosition({
    open: isOpen,
    anchorRef: dropdownRef,
    menuRef,
    margin: MENU_MARGIN,
    deps: [options.length, value],
  });

  const pick = useCallback((option: SelectOption<T>) => {
    if (option.disabled) return;
    onChange(option.value);
    setIsOpen(false);
  }, [onChange]);

  return {
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
  };
}
