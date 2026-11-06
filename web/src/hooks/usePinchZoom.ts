/**
 * 图片预览的手势状态机：滚轮（Ctrl/⌘）/ 双指缩放、单指拖动、双击复位。
 *
 * 两个消费者（文件视图里的内联缩放、全屏灯箱）共用这一份，数学在
 * `shared/zoomTransform.ts`。图表预览另有一套（按 SVG viewBox 做 fit），
 * 暂不并入 —— 它被真机验过，不动。
 *
 * 约定：坐标原点是**容器中心**，内容用 `transform-origin: 0 0` +
 * "先把元素左上角摆到中心、再整体 translate(-50%,-50%)" 的写法居中
 * （与 `DiagramPreview` 同一套，缩放锚点才落在手指下面）。
 */
import { useCallback, useEffect, useRef, useState, type MouseEventHandler, type PointerEventHandler } from "react";
import { gestureMetrics, zoomAt, type ZoomPoint, type ZoomTransform } from "../shared/zoomTransform";

const IDENTITY: ZoomTransform = { x: 0, y: 0, scale: 1 };
/** 双击在「适配」与「2 倍」之间切换。 */
const DOUBLE_TAP_SCALE = 2;

export type PinchZoomBind = {
  onPointerDown: PointerEventHandler<HTMLElement>;
  onPointerMove: PointerEventHandler<HTMLElement>;
  onPointerUp: PointerEventHandler<HTMLElement>;
  onPointerCancel: PointerEventHandler<HTMLElement>;
  onLostPointerCapture: PointerEventHandler<HTMLElement>;
  onDoubleClick: MouseEventHandler<HTMLElement>;
};

export type PinchZoomApi = {
  transform: ZoomTransform;
  /** 已放大/已平移（内联态据此把 touch-action 从 pan-y 切成 none）。 */
  zoomed: boolean;
  zoomBy: (factor: number) => void;
  reset: () => void;
  /** 挂到手势容器上（同时用于量容器矩形）。 */
  surfaceRef: (node: HTMLElement | null) => void;
  bind: PinchZoomBind;
};

export function usePinchZoom(enabled = true): PinchZoomApi {
  const [transform, setTransform] = useState<ZoomTransform>(IDENTITY);
  const [surface, setSurface] = useState<HTMLElement | null>(null);
  const points = useRef(new Map<number, ZoomPoint>());

  const surfaceRef = useCallback((node: HTMLElement | null) => setSurface(node), []);

  const localPoint = useCallback(
    (event: { clientX: number; clientY: number }): ZoomPoint => {
      if (!surface) return { x: 0, y: 0 };
      const rect = surface.getBoundingClientRect();
      return { x: event.clientX - rect.left - rect.width / 2, y: event.clientY - rect.top - rect.height / 2 };
    },
    [surface],
  );

  const zoomBy = useCallback((factor: number) => {
    setTransform((current) => zoomAt(current, factor, { x: 0, y: 0 }));
  }, []);

  const reset = useCallback(() => {
    points.current.clear();
    setTransform(IDENTITY);
  }, []);

  // 滚轮要 preventDefault，必须非 passive；React 的 onWheel 在根上是 passive 的，
  // 所以这里用原生监听器。裸滚轮不接管：那是页面滚动，抢过来会很讨厌。
  useEffect(() => {
    if (!surface || !enabled) return undefined;
    const onWheel = (event: WheelEvent) => {
      if (!event.ctrlKey && !event.metaKey) return;
      event.preventDefault();
      const factor = Math.exp(-Math.max(-100, Math.min(100, event.deltaY)) * 0.01);
      setTransform((current) => zoomAt(current, factor, localPoint(event)));
    };
    surface.addEventListener("wheel", onWheel, { passive: false });
    return () => surface.removeEventListener("wheel", onWheel);
  }, [enabled, localPoint, surface]);

  const release = (event: { pointerId: number }) => {
    points.current.delete(event.pointerId);
  };

  const bind: PinchZoomBind = {
    onPointerDown: (event) => {
      if (!enabled) return;
      if (event.pointerType === "mouse" && event.button !== 0) return;
      event.currentTarget.setPointerCapture(event.pointerId);
      points.current.set(event.pointerId, localPoint(event));
    },
    onPointerMove: (event) => {
      if (!enabled || !points.current.has(event.pointerId)) return;
      const before = gestureMetrics([...points.current.values()]);
      points.current.set(event.pointerId, localPoint(event));
      const after = gestureMetrics([...points.current.values()]);
      const factor = before.distance > 0 && after.distance > 0 ? after.distance / before.distance : 1;
      setTransform((current) => zoomAt(current, factor, before.center, after.center));
    },
    onPointerUp: release,
    onPointerCancel: release,
    onLostPointerCapture: release,
    onDoubleClick: () => {
      if (!enabled) return;
      setTransform((current) => (
        current.scale !== 1 || current.x !== 0 || current.y !== 0
          ? IDENTITY
          : zoomAt(current, DOUBLE_TAP_SCALE, { x: 0, y: 0 })
      ));
    },
  };

  return {
    transform,
    zoomed: transform.scale !== 1 || transform.x !== 0 || transform.y !== 0,
    zoomBy,
    reset,
    surfaceRef,
    bind,
  };
}
