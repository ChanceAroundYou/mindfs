import { useEffect, useRef } from "react";
import { registerBackLayer } from "../services/platform/backNavigation";

/**
 * 把一个覆盖层接进返回栈：开着的时候注册，关掉/卸载时注销。
 * 三个接线点（PanelShell ×3、BottomSheet ×1）只差 open/close 两个值，
 * 所以这里包成一个 hook，别在 App.tsx 里抄三遍 effect。
 */
export function useBackLayer(open: boolean, close: () => void): void {
  // 依赖整个 close：调用点都是内联箭头函数，逐次渲染都是新引用，
  // 按值比较反而会每帧注销重注册。放进 ref 就只在 open 翻转时动栈。
  const closeRef = useRef(close);
  closeRef.current = close;
  useEffect(() => {
    if (!open) return;
    return registerBackLayer({ close: () => closeRef.current() });
  }, [open]);
}
