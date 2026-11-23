import { useEffect, useRef, useState } from "react";
import type { CSSProperties, RefObject } from "react";
import { useAnchoredMenuPosition } from "../../hooks/useAnchoredMenuPosition";

export type WorkflowSelectorMenuState = {
  open: boolean;
  setOpen: (next: boolean) => void;
  triggerRef: RefObject<HTMLDivElement | null>;
  menuRef: RefObject<HTMLDivElement | null>;
  menuStyle: CSSProperties;
};

/**
 * 工作流选择器的开合、锚定与关闭。
 *
 * 任务面板的正文是 `overflow: auto`，绝对定位的菜单会被裁掉 —— 所以菜单
 * portal 到 body、用 `position: fixed` 定位，定位本身交给
 * `useAnchoredMenuPosition`（与 Select / 文件菜单同一套量测与避让）：
 *   - 量触发按钮与菜单的真实尺寸，上下翻面、左右夹取；
 *   - 用 visualViewport 而不是 layout 视口尺寸，软键盘压矮视口时不会被盖住；
 *   - 两级都展开后菜单会向左长高长宽，由 `manualOpen` 触发重算；
 *   - 两侧都放不下时把可用高度写进 maxHeight，菜单内部滚动而不再溢出屏幕。
 *
 * 输入框在面板底部，所以优先向上展开，保留原来的观感。
 */
export function useWorkflowSelectorMenu(manualOpen: boolean): WorkflowSelectorMenuState {
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLDivElement | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);
  const { position } = useAnchoredMenuPosition({
    open,
    anchorRef: triggerRef,
    menuRef,
    align: "right",
    margin: 8,
    prefer: "above",
    deps: [manualOpen],
  });

  useEffect(() => {
    if (!open) return;
    const handlePointerOutside = (event: PointerEvent) => {
      const target = event.target as Node;
      if (triggerRef.current?.contains(target) || menuRef.current?.contains(target)) {
        return;
      }
      setOpen(false);
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", handlePointerOutside);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("pointerdown", handlePointerOutside);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [open]);

  const menuStyle: CSSProperties = {
    position: "fixed",
    top: position?.top ?? 0,
    left: position?.left ?? 0,
    maxHeight: position?.maxHeight,
    maxWidth: position?.maxWidth,
    overflowY: "auto",
    overscrollBehavior: "contain",
    visibility: position ? "visible" : "hidden",
    zIndex: 10100,
  };

  return { open, setOpen, triggerRef, menuRef, menuStyle };
}