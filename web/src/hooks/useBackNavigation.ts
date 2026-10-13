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
 *
 * 关键：这里**不自己消费**，而是走 history.back() —— 让返回键和浏览器后退、
 * iOS 边缘侧滑汇成同一条 popstate 路。以前这里是「关一层 / 退视图」两套逻辑
 * 各走一遍，和 popstate 各自为政，结果一次侧滑关面板、下一次再退视图，
 * 用户看着像「返回要按两下」。统一成 back() 后只有一条路。
 *
 * 没得退时才 preventDefault，把控制权交回系统（退出应用）。
 */
export function installBackNavigation(): () => void {
  const onAndroidBack = (event: Event) => {
    // 覆盖层开着、但没有视图历史可退时**不能**走 back()：那条路只在视图切换时
    // 压过记录，冷启动直接开面板时历史只有一条，back() 是空操作、popstate 压根
    // 不触发 —— 面板就卡在那里关不掉，退出也被 preventDefault 挡死。这种情况直接关层。
    if (hasBackLayer()) {
      event.preventDefault();
      if (hasViewHistoryEntry()) {
        window.history.back();
      } else {
        closeTopBackLayer();
      }
      return;
    }
    if (hasViewHistoryEntry()) {
      event.preventDefault();
      window.history.back();
    }
    // 两边都没有 → 不 preventDefault，把控制权交回系统（退出应用）。
  };

  window.addEventListener(ANDROID_BACK_EVENT, onAndroidBack);
  return () => window.removeEventListener(ANDROID_BACK_EVENT, onAndroidBack);
}

/**
 * 视图历史 = **浏览器历史本身**，不再另起一份数组。
 *
 * 这里曾经有个 viewHistory 数组和 pushState 各记一次「从哪来」，两套栈必然
 * 不同步：一次切换压两条记录，返回一次只弹一条，表现就是「按两下才回得去」。
 * 所以数组整套删掉，只留浏览器历史 —— iOS 边缘侧滑和浏览器后退本来就是
 * history 导航，走它不用另写手势；Android 返回键改成 history.back()，
 * 也就并进了同一条路。
 *
 * 只剩一个计数器：切换视图 pushState 一次 +1，popstate 消费一次 -1。
 * 它的唯一用途是让 Android 返回键知道「有没有得退」——没有就把控制权
 * 交回系统（退出应用）。一个数字，不会和真实历史不同步。
 */
let pendingViewEntries = 0;

/** 切视图时压一条历史。from 是「切之前」那个视图，popstate 靠它退回去。 */
export function pushViewHistoryEntry(from: string): void {
  window.history.pushState({ mindfsView: from }, "");
  pendingViewEntries += 1;
}

/** popstate 消费掉一条我们自己压的记录。别人压的不动。 */
export function consumeViewHistoryEntry(): void {
  if (pendingViewEntries > 0) pendingViewEntries -= 1;
}

/** 还有没有视图可退。 */
export function hasViewHistoryEntry(): boolean {
  return pendingViewEntries > 0;
}
