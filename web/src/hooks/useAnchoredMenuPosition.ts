import { useCallback, useEffect, useLayoutEffect, useRef, useState, type DependencyList, type RefObject } from "react";
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
 *
 * 返回的 maxHeight / maxWidth 要原样写进菜单样式：两侧都放不下时靠它内部滚动，
 * 否则菜单会直接溢出屏幕。
 */
export function useAnchoredMenuPosition({
  open,
  anchorRef,
  menuRef,
  align = "right",
  margin = 8,
  prefer = "below",
  deps = [],
}: {
  open: boolean;
  anchorRef: RefObject<HTMLElement | null>;
  menuRef: RefObject<HTMLElement | null>;
  align?: "left" | "right";
  margin?: number;
  /** 上下都放得下时优先哪一侧；默认下方。 */
  prefer?: "below" | "above";
  /** 菜单内容变化后要重算（长度必须恒定，与 React 的依赖数组规则一致）。 */
  deps?: DependencyList;
}): { position: AnchoredMenuPosition | null; reset: () => void } {
  const [position, setPosition] = useState<AnchoredMenuPosition | null>(null);
  const [tick, setTick] = useState(0);

  // 视口变化时重算（软键盘弹出 / 窗口缩放 / 可视视口平移）
  useEffect(() => {
    if (!open) return;
    const recompute = () => setTick((value) => value + 1);
    window.visualViewport?.addEventListener("resize", recompute);
    window.visualViewport?.addEventListener("scroll", recompute);
    window.addEventListener("resize", recompute);
    window.addEventListener("scroll", recompute, true);
    return () => {
      window.visualViewport?.removeEventListener("resize", recompute);
      window.visualViewport?.removeEventListener("scroll", recompute);
      window.removeEventListener("resize", recompute);
      window.removeEventListener("scroll", recompute, true);
    };
  }, [open]);

  // 菜单自身尺寸变化时重算：二级列表是异步加载的，loading → 列表会让菜单长高，
  // 只靠 deps 抓不到。只在与上次观测不同时才触发，避免观察回调自激。
  const observedSize = useRef({ width: 0, height: 0 });
  useEffect(() => {
    if (!open) return;
    const menu = menuRef.current;
    if (!menu || typeof ResizeObserver === "undefined") return;
    observedSize.current = { width: menu.offsetWidth, height: menu.offsetHeight };
    const observer = new ResizeObserver((entries) => {
      const entry = entries[0];
      if (!entry) return;
      const next = { width: entry.contentRect.width, height: entry.contentRect.height };
      const previous = observedSize.current;
      if (previous.width === next.width && previous.height === next.height) return;
      observedSize.current = next;
      setTick((value) => value + 1);
    });
    observer.observe(menu);
    return () => observer.disconnect();
  }, [open, menuRef]);

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
      prefer,
    });
    setPosition((current) => (samePosition(current, next) ? current : next));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, align, margin, prefer, tick, ...deps]);

  const reset = useCallback(() => setPosition(null), []);
  return { position, reset };
}

function samePosition(a: AnchoredMenuPosition | null, b: AnchoredMenuPosition): boolean {
  if (!a) return false;
  return (
    Math.abs(a.top - b.top) < 0.5 &&
    Math.abs(a.left - b.left) < 0.5 &&
    Math.abs(a.maxHeight - b.maxHeight) < 0.5 &&
    Math.abs(a.maxWidth - b.maxWidth) < 0.5
  );
}