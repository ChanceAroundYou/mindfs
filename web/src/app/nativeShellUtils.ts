import { isHarmonyRuntime, isNativeShellRuntime } from "../services/platform/runtime";





export function isNativeLauncherOrigin(): boolean {
  if (!isNativeShellRuntime() || typeof window === "undefined") {
    return false;
  }
  const hostname = window.location.hostname.toLowerCase();
  if (isHarmonyRuntime()) {
    return hostname === "mindfs.local";
  }
  return hostname === "localhost" || hostname === "127.0.0.1" || hostname === "mindfs.local";
}





export function nativeLauncherURL(): string {
  if (typeof window === "undefined") {
    return "http://localhost";
  }
  if (isHarmonyRuntime()) {
    return "https://mindfs.local/index.html";
  }
  const hostname = window.location.hostname.toLowerCase();
  if (hostname === "mindfs.local") {
    return "https://mindfs.local/index.html";
  }
  return "http://localhost";
}





export function normalizeSystemBarColor(input: string, fallback: string): string {
  const value = input.trim();
  if (!value) {
    return fallback;
  }
  if (/^#[0-9a-f]{6}$/i.test(value)) {
    return value.toLowerCase();
  }
  const rgbMatch = value.match(/^rgba?\(([^)]+)\)$/i);
  if (!rgbMatch) {
    return fallback;
  }
  const parts = rgbMatch[1]
    .split(",")
    .slice(0, 3)
    .map((part) => Number.parseInt(part.trim(), 10));
  if (parts.length !== 3 || parts.some((part) => Number.isNaN(part) || part < 0 || part > 255)) {
    return fallback;
  }
  return `#${parts.map((part) => part.toString(16).padStart(2, "0")).join("")}`;
}





export function parseColorToHex(input: string): string | null {
  const value = input.trim();
  if (!value || value === "transparent") {
    return null;
  }
  if (/^#[0-9a-f]{6}$/i.test(value)) {
    return value.toLowerCase();
  }
  const rgbMatch = value.match(/^rgba?\(([^)]+)\)$/i);
  if (!rgbMatch) {
    return null;
  }
  const parts = rgbMatch[1]
    .split(",")
    .slice(0, 4)
    .map((part) => Number.parseFloat(part.trim()));
  if (parts.length < 3 || parts.slice(0, 3).some((part) => Number.isNaN(part) || part < 0 || part > 255)) {
    return null;
  }
  const alpha = parts[3] ?? 1;
  if (alpha <= 0.01) {
    return null;
  }
  return `#${parts.slice(0, 3).map((part) => Math.round(part).toString(16).padStart(2, "0")).join("")}`;
}





export function isDarkColor(hexColor: string): boolean {
  const hex = hexColor.replace("#", "");
  const r = Number.parseInt(hex.slice(0, 2), 16) / 255;
  const g = Number.parseInt(hex.slice(2, 4), 16) / 255;
  const b = Number.parseInt(hex.slice(4, 6), 16) / 255;
  const [lr, lg, lb] = [r, g, b].map((channel) =>
    channel <= 0.03928 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4
  );
  const luminance = 0.2126 * lr + 0.7152 * lg + 0.0722 * lb;
  return luminance < 0.5;
}





export function resolveTopVisibleColor(): string | null {
  if (typeof window === "undefined" || typeof document === "undefined") {
    return null;
  }
  const safeTopRaw = getComputedStyle(document.documentElement)
    .getPropertyValue("--mindfs-safe-area-top")
    .trim();
  const safeTop = Number.parseFloat(safeTopRaw) || 0;
  const sampleX = Math.max(1, Math.min(window.innerWidth - 1, window.innerWidth / 2));
  const sampleY = Math.max(1, Math.min(window.innerHeight - 1, safeTop + 8));
  let element = document.elementFromPoint(sampleX, sampleY) as HTMLElement | null;
  while (element) {
    const styles = window.getComputedStyle(element);
    const backgroundColor = parseColorToHex(styles.backgroundColor);
    if (backgroundColor) {
      return backgroundColor;
    }
    element = element.parentElement;
  }
  return null;
}





export function isTextEditableTarget(target: EventTarget | null): target is HTMLElement {
  if (!(target instanceof HTMLElement)) {
    return false;
  }
  const tag = target.tagName;
  return (
    tag === "INPUT" ||
    tag === "TEXTAREA" ||
    target.isContentEditable
  );
}





export function isIOSWebKit(): boolean {
  if (typeof navigator === "undefined") {
    return false;
  }
  const userAgent = navigator.userAgent || "";
  return /iP(hone|ad|od)/.test(userAgent) || (
    navigator.platform === "MacIntel" &&
    Number((navigator as Navigator & { maxTouchPoints?: number }).maxTouchPoints || 0) > 1
  );
}





export let largestNativeViewportHeight = 0;


type VirtualKeyboardLike = {
  overlaysContent: boolean;
  boundingRect?: { height?: number };
  addEventListener?: (type: string, listener: () => void) => void;
  removeEventListener?: (type: string, listener: () => void) => void;
};


function readVirtualKeyboard(): VirtualKeyboardLike | null {
  if (typeof navigator === "undefined") {
    return null;
  }
  const virtualKeyboard = (navigator as Navigator & { virtualKeyboard?: VirtualKeyboardLike }).virtualKeyboard;
  return virtualKeyboard && typeof virtualKeyboard.overlaysContent === "boolean" ? virtualKeyboard : null;
}


function readVirtualKeyboardImeBottom(virtualKeyboard: VirtualKeyboardLike): number {
  const height = virtualKeyboard.boundingRect?.height;
  return typeof height === "number" && Number.isFinite(height) ? Math.max(0, height) : 0;
}


/**
 * 让键盘**覆盖**内容，而不是把视口挤小。
 *
 * 装成 PWA（Edge/Chrome 添加到主屏）后，键盘弹出会把视觉视口挤小：页面只画到视觉视口
 * 底边，下面那段（窗口里、页面外）由**壳自己的窗口底色**填充 —— 而那份底色是安装时从
 * manifest 的 background_color 烧进 WebAPK 的，运行时改不了（meta theme-color 管不到它），
 * 于是暗色主题闪白、浅色主题闪黑，怎么选 manifest 都是错的。
 *
 * overlaysContent 让浏览器不缩视口：页面始终铺满窗口，键盘盖在上面，那条缝根本不存在。
 * 代价是键盘不再自动把输入框顶上来 —— 由 syncViewportHeight 用 boundingRect 自己抬
 * （与原生壳的 --mindfs-ime-bottom 同一套算法）。浏览器不支持时原样不动。
 */
export function installVirtualKeyboardOverlay(): () => void {
  const virtualKeyboard = readVirtualKeyboard();
  if (!virtualKeyboard || isNativeShellRuntime()) {
    return () => {};
  }
  try {
    virtualKeyboard.overlaysContent = true;
  } catch {
    return () => {};
  }
  const onChange = () => syncViewportHeightAfterKeyboardChange();
  virtualKeyboard.addEventListener?.("geometrychange", onChange);
  syncViewportHeight();
  return () => virtualKeyboard.removeEventListener?.("geometrychange", onChange);
}





export function readRootPixelVar(name: string): number {
  if (typeof document === "undefined") {
    return 0;
  }
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  const parsed = Number.parseFloat(value);
  return Number.isFinite(parsed) ? Math.max(0, parsed) : 0;
}





/**
 * 键盘盖在内容上时，视口高度该扣多少：只扣「还没被视口缩小吃掉」的那部分键盘高度。
 * resizedBy 已经缩掉的部分不能再扣一次 —— 否则输入框会被顶高一整个键盘。
 * 两个来源共用同一套换算：原生壳注入的 --mindfs-ime-bottom、浏览器的 VirtualKeyboard
 * boundingRect（overlaysContent 已开，视口不缩，全靠这里抬）。
 */
export function keyboardAdjustedViewportHeight(input: {
  rawViewportHeight: number;
  largestViewportHeight: number;
  imeBottom: number;
}): number {
  const resizedBy = Math.max(0, input.largestViewportHeight - input.rawViewportHeight);
  const remainingImeOverlay = Math.max(0, input.imeBottom - resizedBy);
  return Math.max(320, input.rawViewportHeight - remainingImeOverlay);
}


export function syncViewportHeight(): void {
  if (typeof window === "undefined" || typeof document === "undefined") {
    return;
  }

  const visualViewport = window.visualViewport;
  const isNativeShell = isNativeShellRuntime();
  const virtualKeyboard = isNativeShell ? null : readVirtualKeyboard();
  const rawViewportHeight = isNativeShell || !visualViewport
    ? window.innerHeight
    : visualViewport.height;
  let viewportHeight = rawViewportHeight;
  if (isNativeShell || virtualKeyboard) {
    // 原生壳报的是注入的 IME 内边距；浏览器里由 VirtualKeyboard API 报键盘高度。
    const imeBottom = isNativeShell
      ? readRootPixelVar("--mindfs-ime-bottom")
      : readVirtualKeyboardImeBottom(virtualKeyboard as VirtualKeyboardLike);
    if (imeBottom > 0) {
      largestNativeViewportHeight ||= rawViewportHeight;
    } else {
      largestNativeViewportHeight = rawViewportHeight;
    }
    viewportHeight = keyboardAdjustedViewportHeight({
      rawViewportHeight,
      largestViewportHeight: largestNativeViewportHeight,
      imeBottom,
    });
  }
  const viewportOffsetTop = isNativeShell || !visualViewport
    ? 0
    : Math.max(0, visualViewport.offsetTop || 0);
  document.documentElement.style.setProperty("--mindfs-viewport-height", `${viewportHeight}px`);
  document.documentElement.style.setProperty("--mindfs-viewport-offset-top", `${viewportOffsetTop}px`);
}





export function syncViewportHeightAfterKeyboardChange(): void {
  syncViewportHeight();
  window.scrollTo(0, 0);
  window.requestAnimationFrame(syncViewportHeight);
  window.requestAnimationFrame(() => window.scrollTo(0, 0));
  window.setTimeout(syncViewportHeight, 120);
  window.setTimeout(() => window.scrollTo(0, 0), 120);
  window.setTimeout(syncViewportHeight, 320);
  window.setTimeout(() => window.scrollTo(0, 0), 320);
}





export function canScrollTouchTarget(target: EventTarget | null, deltaY: number): boolean {
  if (!(target instanceof Element) || deltaY === 0) {
    return false;
  }

  let element: Element | null = target;
  while (element && element !== document.documentElement && element !== document.body) {
    const styles = window.getComputedStyle(element);
    const overflowY = styles.overflowY;
    const canScroll = (overflowY === "auto" || overflowY === "scroll") &&
      element.scrollHeight > element.clientHeight + 1;
    if (canScroll) {
      const scrollTop = element.scrollTop;
      const maxScrollTop = element.scrollHeight - element.clientHeight;
      if (deltaY > 0 && scrollTop > 0) {
        return true;
      }
      if (deltaY < 0 && scrollTop < maxScrollTop - 1) {
        return true;
      }
    }
    element = element.parentElement;
  }
  return false;
}





export function installIOSKeyboardPanLock(): () => void {
  if (!isIOSWebKit()) {
    return () => {};
  }

  let lastTouchY = 0;
  const onTouchStart = (event: TouchEvent) => {
    lastTouchY = event.touches[0]?.clientY || 0;
  };
  const onTouchMove = (event: TouchEvent) => {
    const activeElement = document.activeElement;
    if (!isTextEditableTarget(activeElement) || event.touches.length !== 1) {
      return;
    }

    const nextTouchY = event.touches[0]?.clientY || lastTouchY;
    const deltaY = nextTouchY - lastTouchY;
    lastTouchY = nextTouchY;
    if (canScrollTouchTarget(event.target, deltaY)) {
      return;
    }
    event.preventDefault();
    window.scrollTo(0, 0);
  };

  document.addEventListener("touchstart", onTouchStart, { passive: true });
  document.addEventListener("touchmove", onTouchMove, { passive: false });
  return () => {
    document.removeEventListener("touchstart", onTouchStart);
    document.removeEventListener("touchmove", onTouchMove);
  };
}
