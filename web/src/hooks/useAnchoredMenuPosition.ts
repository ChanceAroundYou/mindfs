import { useCallback, useEffect, useLayoutEffect, useState, type DependencyList, type RefObject } from "react";
import {
  computeAnchoredMenuPosition,
  type AnchoredMenuPosition,
} from "../shared/anchoredMenuPosition";

/**
 * 锚定弹层的位置：测量锚点与菜单，按 shared/anchoredMenuPosition 的规则算出
 * 视口内的位置，并在视口变化（缩放 / 滚动 / 软键盘）时重算。
 *
 * 菜单必须 portal 到 body 再用 fixed 定位（原因见 shared/anchoredMenuPosition），
 * 所以这里的坐标是**视口坐标**，不是相对某个容器。
 */
export function useAnchoredMenuPosition({
  open,
  anchorRef,
  menuRef,
  align = "right",
  margin = 8,
  deps = [],
}: {
  open: boolean;
  anchorRef: RefObject<HTMLElement | null>;
  menuRef: RefObject<HTMLElement | null>;
  align?: "left" | "right";
  margin?: number;
  /** 菜单内容变化后要重算（长度必须恒定，与 React 的依赖数组规则一致）。 */
  deps?: DependencyList;
}): { position: AnchoredMenuPosition | null; reset: () => void } {
  const [position, setPosition] = useState<AnchoredMenuPosition | null>(null);
  const [tick, setTick] = useState(0);

  // 视口变化时重算（软键盘弹出 / 窗口缩放会改变可用高度）
  useEffect(() => {
    if (!open) return;
    const recompute = () => setTick((value) => value + 1);
    window.visualViewport?.addEventListener("resize", recompute);
    window.addEventListener("resize", recompute);
    window.addEventListener("scroll", recompute, true);
    return () => {
      window.visualViewport?.removeEventListener("resize", recompute);
      window.removeEventListener("resize", recompute);
      window.removeEventListener("scroll", recompute, true);
    };
  }, [open]);

  useLayoutEffect(() => {
    if (!open) {
      setPosition(null);
      return;
    }
    const anchor = anchorRef.current;
    const menu = menuRef.current;
    if (!anchor || !menu) return;
    const viewport = window.visualViewport;
    const next = computeAnchoredMenuPosition({
      anchor: anchor.getBoundingClientRect(),
      menu: menu.getBoundingClientRect(),
      viewport: {
        top: viewport?.offsetTop ?? 0,
        left: viewport?.offsetLeft ?? 0,
        width: viewport?.width ?? window.innerWidth,
        height: viewport?.height ?? window.innerHeight,
      },
      align,
      margin,
    });
    setPosition((current) =>
      current && Math.abs(current.top - next.top) < 0.5 && Math.abs(current.left - next.left) < 0.5
        ? current
        : next,
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, align, margin, tick, ...deps]);

  const reset = useCallback(() => setPosition(null), []);
  return { position, reset };
}
