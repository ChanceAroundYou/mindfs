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





export function readRootPixelVar(name: string): number {
  if (typeof document === "undefined") {
    return 0;
  }
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  const parsed = Number.parseFloat(value);
  return Number.isFinite(parsed) ? Math.max(0, parsed) : 0;
}





let viewportHeightAnimation: number | null = null;

export function animateViewportHeight(target: number): void {
  if (typeof document === "undefined") {
    return;
  }
  const current = readRootPixelVar("--mindfs-viewport-height") || target;
  if (Math.abs(current - target) < 2) {
    document.documentElement.style.setProperty("--mindfs-viewport-height", `${target}px`);
    return;
  }
  if (viewportHeightAnimation !== null) {
    cancelAnimationFrame(viewportHeightAnimation);
    viewportHeightAnimation = null;
  }
  const start = current;
  const startTime = performance.now();
  const duration = 180;
  const step = (now: number) => {
    const progress = Math.min(1, (now - startTime) / duration);
    const eased = 1 - Math.pow(1 - progress, 3);
    const height = start + (target - start) * eased;
    document.documentElement.style.setProperty("--mindfs-viewport-height", `${height}px`);
    if (progress < 1) {
      viewportHeightAnimation = requestAnimationFrame(step);
    } else {
      viewportHeightAnimation = null;
      document.documentElement.style.setProperty("--mindfs-viewport-height", `${target}px`);
    }
  };
  viewportHeightAnimation = requestAnimationFrame(step);
}

export function syncViewportHeight(): void {
  if (typeof window === "undefined" || typeof document === "undefined") {
    return;
  }

  const visualViewport = window.visualViewport;
  const isNativeShell = isNativeShellRuntime();
  const rawViewportHeight = isNativeShell || !visualViewport
    ? window.innerHeight
    : visualViewport.height;
  let viewportHeight = rawViewportHeight;
  if (isNativeShell) {
    const imeBottom = readRootPixelVar("--mindfs-ime-bottom");
    if (imeBottom > 0) {
      largestNativeViewportHeight ||= rawViewportHeight;
    } else {
      largestNativeViewportHeight = rawViewportHeight;
    }
    const resizedBy = Math.max(0, largestNativeViewportHeight - rawViewportHeight);
    const remainingImeOverlay = Math.max(0, imeBottom - resizedBy);
    viewportHeight = Math.max(320, rawViewportHeight - remainingImeOverlay);
  }
  const viewportOffsetTop = isNativeShell || !visualViewport
    ? 0
    : Math.max(0, visualViewport.offsetTop || 0);
  animateViewportHeight(viewportHeight);
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
