import { useCallback, useEffect, useState } from "react";
import { isNativeShellRuntime } from "../services/platform/runtime";
import { isNativeLauncherOrigin, nativeLauncherURL, normalizeSystemBarColor, parseColorToHex, isDarkColor, resolveTopVisibleColor, isTextEditableTarget, isIOSWebKit, readRootPixelVar, syncViewportHeight, syncViewportHeightAfterKeyboardChange, canScrollTouchTarget, installIOSKeyboardPanLock } from "./nativeShellUtils";

export function useNativeShellLifecycle() {
  const [ready] = useState(() => !isNativeLauncherOrigin());
  const goToLauncher = useCallback(() => {
    if (typeof window === "undefined") return;
    window.location.assign(nativeLauncherURL());
  }, []);



  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }

    syncViewportHeight();
    window.addEventListener("resize", syncViewportHeight);
    window.addEventListener("orientationchange", syncViewportHeight);
    window.addEventListener("focusin", syncViewportHeightAfterKeyboardChange);
    window.addEventListener("focusout", syncViewportHeightAfterKeyboardChange);
    window.addEventListener("mindfs:safe-area-updated", syncViewportHeight as EventListener);
    window.visualViewport?.addEventListener("resize", syncViewportHeight);
    window.visualViewport?.addEventListener("scroll", syncViewportHeight);
    const uninstallIOSKeyboardPanLock = installIOSKeyboardPanLock();

    if (!isNativeShellRuntime()) {
      return () => {
        window.removeEventListener("resize", syncViewportHeight);
        window.removeEventListener("orientationchange", syncViewportHeight);
        window.removeEventListener("focusin", syncViewportHeightAfterKeyboardChange);
        window.removeEventListener("focusout", syncViewportHeightAfterKeyboardChange);
        window.removeEventListener("mindfs:safe-area-updated", syncViewportHeight as EventListener);
        window.visualViewport?.removeEventListener("resize", syncViewportHeight);
        window.visualViewport?.removeEventListener("scroll", syncViewportHeight);
        uninstallIOSKeyboardPanLock();
      };
    }

    const nativeCapacitor = (window as Window & {
      Capacitor?: { isPluginAvailable?: (name: string) => boolean };
    }).Capacitor;
    const hasPlugin = (name: string) =>
      typeof nativeCapacitor?.isPluginAvailable === "function"
        ? nativeCapacitor.isPluginAvailable(name)
        : false;

    let cleanupThemeSync: (() => void) | undefined;

    if (hasPlugin("StatusBar")) {
      void import("@capacitor/status-bar")
        .then(async ({ StatusBar, Style }) => {
          try {
            await StatusBar.setOverlaysWebView({ overlay: true });
            let syncQueued = false;
            const syncStatusBar = async () => {
              const fallback = window.matchMedia("(prefers-color-scheme: dark)").matches
                ? "#020617"
                : "#ffffff";
              const rootStyles = getComputedStyle(document.documentElement);
              const color = resolveTopVisibleColor()
                || normalizeSystemBarColor(rootStyles.getPropertyValue("--mindfs-system-bar-bg"), fallback);
              await StatusBar.setStyle({
                style: isDarkColor(color) ? Style.Dark : Style.Light,
              });
              await StatusBar.setBackgroundColor({
                color,
              });
            };
            const queueStatusBarSync = () => {
              if (syncQueued) {
                return;
              }
              syncQueued = true;
              window.requestAnimationFrame(() => {
                syncQueued = false;
                void syncStatusBar();
              });
            };
            await syncStatusBar();

            const media = window.matchMedia("(prefers-color-scheme: dark)");
            const handleChange = () => {
              queueStatusBarSync();
            };
            const observer = new MutationObserver(() => {
              queueStatusBarSync();
            });
            observer.observe(document.documentElement, {
              attributes: true,
              attributeFilter: ["class", "style", "data-theme"],
            });
            observer.observe(document.body, {
              attributes: true,
              childList: true,
              subtree: true,
              attributeFilter: ["class", "style", "data-theme"],
            });
            window.addEventListener("pageshow", handleChange);
            window.addEventListener("focus", handleChange);
            window.addEventListener("resize", handleChange);
            window.visualViewport?.addEventListener("resize", handleChange);
            window.visualViewport?.addEventListener("scroll", handleChange);
            window.addEventListener("scroll", handleChange, { passive: true });
            document.addEventListener("visibilitychange", handleChange);
            window.addEventListener("mindfs:safe-area-updated", handleChange as EventListener);
            window.addEventListener("mindfs:native-theme-changed", handleChange as EventListener);
            if (typeof media.addEventListener === "function") {
              media.addEventListener("change", handleChange);
              cleanupThemeSync = () => {
                media.removeEventListener("change", handleChange);
                window.removeEventListener("pageshow", handleChange);
                window.removeEventListener("focus", handleChange);
                window.removeEventListener("resize", handleChange);
                window.visualViewport?.removeEventListener("resize", handleChange);
                window.visualViewport?.removeEventListener("scroll", handleChange);
                window.removeEventListener("scroll", handleChange);
                document.removeEventListener("visibilitychange", handleChange);
                window.removeEventListener("mindfs:safe-area-updated", handleChange as EventListener);
                window.removeEventListener("mindfs:native-theme-changed", handleChange as EventListener);
                observer.disconnect();
              };
            } else {
              media.addListener(handleChange);
              cleanupThemeSync = () => {
                media.removeListener(handleChange);
                window.removeEventListener("pageshow", handleChange);
                window.removeEventListener("focus", handleChange);
                window.removeEventListener("resize", handleChange);
                window.visualViewport?.removeEventListener("resize", handleChange);
                window.visualViewport?.removeEventListener("scroll", handleChange);
                window.removeEventListener("scroll", handleChange);
                document.removeEventListener("visibilitychange", handleChange);
                window.removeEventListener("mindfs:safe-area-updated", handleChange as EventListener);
                window.removeEventListener("mindfs:native-theme-changed", handleChange as EventListener);
                observer.disconnect();
              };
            }
          } catch (error) {
            console.warn("[capacitor-status-bar] overlay unavailable", error);
          }
        })
        .catch((error) => {
          console.warn("[capacitor-status-bar] failed to load plugin", error);
        });
    }

    const scrollFocusedIntoView = () => {
      if (isIOSWebKit()) {
        syncViewportHeightAfterKeyboardChange();
        return;
      }
      const target = document.activeElement;
      if (!isTextEditableTarget(target)) {
        return;
      }
      window.setTimeout(() => {
        try {
          target.scrollIntoView({ block: "nearest", inline: "nearest", behavior: "smooth" });
        } catch {
          target.scrollIntoView();
        }
      }, 180);
    };

    const onFocusIn = (event: FocusEvent) => {
      if (isTextEditableTarget(event.target)) {
        scrollFocusedIntoView();
      }
    };
    document.addEventListener("focusin", onFocusIn);
    const onViewportResize = () => {
      scrollFocusedIntoView();
    };
    window.visualViewport?.addEventListener("resize", onViewportResize);

    const onBackRequest = () => {
      // cancelable：应用内有层可关时监听方要 preventDefault 挡住 Capacitor 的
      // 默认退出。事件不可取消的话 preventDefault 静默失效，按一次返回会
      // 「先关掉面板、紧接着整个应用退出」。
      const event = new CustomEvent("mindfs:android-back-request", { cancelable: true });
      window.dispatchEvent(event);
    };

    let removeListener: (() => void) | undefined;
    if (hasPlugin("App")) {
      void import("@capacitor/app")
        .then(async ({ App: CapApp }) => {
          try {
            const handle = await CapApp.addListener("backButton", () => {
              onBackRequest();
            });
            removeListener = () => {
              void handle.remove();
            };
          } catch (error) {
            console.warn("[capacitor-app] backButton listener unavailable", error);
          }
        })
        .catch((error) => {
          console.warn("[capacitor-app] failed to load plugin", error);
        });
    }

    return () => {
      window.removeEventListener("resize", syncViewportHeight);
      window.removeEventListener("orientationchange", syncViewportHeight);
      window.removeEventListener("focusin", syncViewportHeightAfterKeyboardChange);
      window.removeEventListener("focusout", syncViewportHeightAfterKeyboardChange);
      window.removeEventListener("mindfs:safe-area-updated", syncViewportHeight as EventListener);
      window.visualViewport?.removeEventListener("resize", syncViewportHeight);
      window.visualViewport?.removeEventListener("scroll", syncViewportHeight);
      cleanupThemeSync?.();
      document.removeEventListener("focusin", onFocusIn);
      window.visualViewport?.removeEventListener("resize", onViewportResize);
      uninstallIOSKeyboardPanLock();
      removeListener?.();
    };
  }, []);

  return { ready, goToLauncher };
}
