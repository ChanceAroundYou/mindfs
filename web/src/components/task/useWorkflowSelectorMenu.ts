import { useEffect, useRef, useState } from "react";
import type { CSSProperties, RefObject } from "react";

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
 * 任务详情面板的正文是 `overflow: auto`，绝对定位的菜单会被裁掉 —— 所以菜单
 * portal 到 body、用 `position: fixed` 贴在触发按钮上方（输入框在面板底部，
 * 往上弹比往下弹更不容易出屏）。定位算法与 AgentSelector 的 viewport 模式同源：
 * 打开时按触发按钮的 rect 算一次，窗口/可视视口变化时重算。
 */
export function useWorkflowSelectorMenu(): WorkflowSelectorMenuState {
  const [open, setOpen] = useState(false);
  const [anchor, setAnchor] = useState<{ bottom: number; right: number } | null>(null);
  const triggerRef = useRef<HTMLDivElement | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!open) return;
    const recompute = () => {
      const trigger = triggerRef.current;
      if (!trigger) return;
      const rect = trigger.getBoundingClientRect();
      setAnchor({
        bottom: Math.max(8, window.innerHeight - rect.top + 8),
        right: Math.max(8, window.innerWidth - rect.right),
      });
    };
    recompute();
    window.addEventListener("resize", recompute);
    window.visualViewport?.addEventListener("resize", recompute);
    return () => {
      window.removeEventListener("resize", recompute);
      window.visualViewport?.removeEventListener("resize", recompute);
    };
  }, [open]);

  useEffect(() => {
    if (!open) {
      setAnchor(null);
      return;
    }
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
    bottom: anchor?.bottom ?? 0,
    right: anchor?.right ?? 0,
    visibility: anchor ? "visible" : "hidden",
    zIndex: 10100,
  };

  return { open, setOpen, triggerRef, menuRef, menuStyle };
}