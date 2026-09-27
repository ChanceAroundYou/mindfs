/**
 * 「返回上一级」的分发器。
 *
 * 以前没有任何地方处理返回：Android 硬件返回键派发了
 * `mindfs:android-back-request` 事件但全项目零监听，浏览器历史只有一个条目
 * （switchMainView 全程 replaceState），iOS 的边缘侧滑又被 html 的
 * overscroll-behavior: none 关掉了 —— 于是「返回」只能退出应用。
 *
 * 这里只做一件事：按 **覆盖层优先、视图栈兜底** 的顺序把一次「返回」路由掉，
 * 真正无处可退时才返回 false，让调用方把控制权交回系统。
 *
 * 刻意不做的：z-index 推导、焦点陷阱、dialogService 改造成栈。
 * 各层自己接既有关闭路径（PanelShell.requestClose / BottomSheet 的 onClose /
 * dialogService.dismiss），所以「有未保存改动要确认」这类既有逻辑自动保留。
 */

import { useEffect, useRef } from "react";

export type BackLayer = {
  /** 关掉这一层。返回后本层会被自动注销。 */
  close: () => void;
};

export const ANDROID_BACK_EVENT = "mindfs:android-back-request";

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

/** 后注册的在最上面。不用 z-index 推算：谁最后挂载谁在最上面，和 DOM 顺序一致。 */
const layers: BackLayer[] = [];

/** 同一层组件可能反复挂载/卸载，用它避免注销掉别人刚注册的一层。 */
let unregister: (() => void) | null = null;

/**
 * 注册一个可被「返回」关闭的覆盖层，返回注销函数。
 * 卸载时必须调用，否则关闭后再按返回会打到已经消失的层上。
 */
export function registerBackLayer(layer: BackLayer): () => void {
  layers.push(layer);
  let active = true;
  return () => {
    if (!active) return;
    active = false;
    const index = layers.lastIndexOf(layer);
    if (index >= 0) layers.splice(index, 1);
  };
}

/** 覆盖层栈非空 = 至少有一层可关。 */
export function hasBackLayer(): boolean {
  return layers.length > 0;
}

/** 关掉最上面那一层。调用方负责处理返回值。 */
export function closeTopBackLayer(): boolean {
  const top = layers[layers.length - 1];
  if (!top) return false;
  // 先摘再关：close() 可能同步触发 setState 让组件卸载、
  // 连带执行本层的注销函数，栈里留着这一条会被重复处理。
  layers.pop();
  top.close();
  return true;
}

/**
 * 装一次全局返回监听（Android 硬件返回键）。
 * onFallback 是「覆盖层栈空着」时的下一级路由，返回 true 表示**它处理了** ——
 * 处理了就得 preventDefault，否则 Capacitor 的默认退出照样发生，
 * 表现是「按返回切了视图，紧接着整个应用退出」。
 * 返回 false（真的无处可退）才把控制权交回系统。
 */
export function installBackNavigation(onFallback: () => boolean): () => void {
  const onAndroidBack = (event: Event) => {
    if (hasBackLayer()) {
      event.preventDefault();
      closeTopBackLayer();
      return;
    }
    if (onFallback()) {
      event.preventDefault();
    }
  };

  window.addEventListener(ANDROID_BACK_EVENT, onAndroidBack);
  return () => window.removeEventListener(ANDROID_BACK_EVENT, onAndroidBack);
}

/**
 * 视图历史栈（瞬态，不落盘）。
 *
 * 和「记住用户上次选的视图」是两件事：那个落 localStorage、跨会话；
 * 这个只在本次运行内让「返回」有路可退。切到同一个视图不记。
 */
const VIEW_HISTORY_LIMIT = 20;
const viewHistory: string[] = [];

/** 记下「从哪来」。切到同一视图不算一次返回目标。 */
export function pushViewHistory(view: string): void {
  if (viewHistory[viewHistory.length - 1] === view) return;
  viewHistory.push(view);
  // 栈深上限：防止长时间使用后一次返回要连按几十下才回到起点。
  if (viewHistory.length > VIEW_HISTORY_LIMIT) viewHistory.shift();
}

export function hasViewHistory(): boolean {
  return viewHistory.length > 0;
}

/** 弹出上一个视图，没有则返回 null。 */
export function popViewHistory(): string | null {
  return viewHistory.pop() ?? null;
}
