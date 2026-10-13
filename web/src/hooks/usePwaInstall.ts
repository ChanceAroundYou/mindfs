import { useCallback, useEffect, useMemo, useState } from "react";
import { isNativeShellRuntime } from "../services/platform/runtime";
import { pwaInstallService } from "../services/platform/pwaInstall";
import { alertDialog } from "../services/dialog";

type BeforeInstallPromptEvent = Event & {
  prompt: () => Promise<void>;
  userChoice: Promise<{ outcome: "accepted" | "dismissed"; platform: string }>;
};

const PWA_INSTALL_STATE_KEY = "mindfs:pwa-installed";

type Translate = (key: Parameters<import("../i18n").I18nContextValue["t"]>[0]) => string;

export function usePwaInstall(t: Translate) {
  const [deferredInstallPrompt, setDeferredInstallPrompt] = useState<BeforeInstallPromptEvent | null>(null);
  const [isInstalled, setIsInstalled] = useState(false);
  const [isInstallCapable, setIsInstallCapable] = useState(false);
  const [installProbeDone, setInstallProbeDone] = useState(false);
  const [isNativeApp, setIsNativeApp] = useState(() => isNativeShellRuntime());

  const isIOS = useMemo(() => {
    if (typeof window === "undefined") return false;
    const ua = window.navigator.userAgent;
    return /iPad|iPhone|iPod/.test(ua) || (ua.includes("Mac") && "ontouchend" in document);
  }, []);
  const isMacSafari = useMemo(() => {
    if (typeof window === "undefined") return false;
    const ua = window.navigator.userAgent;
    return ua.includes("Macintosh") && /Safari/.test(ua) && !/Chrome|Chromium|Edg|OPR|CriOS|FxiOS/.test(ua);
  }, []);
  const isAndroidChrome = useMemo(() => {
    if (typeof window === "undefined") return false;
    const ua = window.navigator.userAgent;
    return /Android/i.test(ua) && /Chrome|Chromium/i.test(ua) && !/EdgA|OPR|SamsungBrowser|Firefox|QQBrowser|MQQBrowser|UCBrowser|HuaweiBrowser|MiuiBrowser|VivoBrowser|HeyTapBrowser/i.test(ua);
  }, []);
  const isDesktopChromium = useMemo(() => {
    if (typeof window === "undefined") return false;
    const ua = window.navigator.userAgent;
    return !/Android|iPhone|iPad|iPod/i.test(ua) && /Chrome|Chromium|Edg/i.test(ua) && !/OPR/i.test(ua);
  }, []);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const refreshNativeRuntime = () => setIsNativeApp(isNativeShellRuntime());
    refreshNativeRuntime();
    const timers = [250, 1000, 2000].map((delay) => window.setTimeout(refreshNativeRuntime, delay));
    window.addEventListener("mindfs:native-bridge-ready", refreshNativeRuntime);
    window.addEventListener("pageshow", refreshNativeRuntime);
    window.addEventListener("focus", refreshNativeRuntime);
    return () => {
      timers.forEach((timer) => window.clearTimeout(timer));
      window.removeEventListener("mindfs:native-bridge-ready", refreshNativeRuntime);
      window.removeEventListener("pageshow", refreshNativeRuntime);
      window.removeEventListener("focus", refreshNativeRuntime);
    };
  }, []);

  useEffect(() => pwaInstallService.subscribe((snapshot) => {
    setDeferredInstallPrompt(snapshot.deferredPrompt);
    setIsInstalled(snapshot.installed);
    setInstallProbeDone(snapshot.probed);
    setIsInstallCapable(
      snapshot.deferredPrompt !== null || snapshot.installed || isIOS ||
      (typeof navigator !== "undefined" && "serviceWorker" in navigator),
    );
  }), [isIOS]);

  const hasPersistedInstallState = useCallback(() => {
    if (typeof window === "undefined") return false;
    try {
      return window.localStorage.getItem(PWA_INSTALL_STATE_KEY) === "true";
    } catch {
      return false;
    }
  }, []);
  const isKnownInstalled = isInstalled || hasPersistedInstallState();
  const installLabel = isKnownInstalled
    ? t("pwa.installed")
    : isIOS
      ? t("pwa.addToHomeScreen")
      : isMacSafari
        ? t("pwa.addToDock")
        : isDesktopChromium && isInstallCapable
          ? t("pwa.installApp")
          : isAndroidChrome && !deferredInstallPrompt
            ? t("pwa.installFromMenu")
            : deferredInstallPrompt
              ? t("pwa.installApp")
              : t("pwa.installInstructions");
  const installHelp = isInstalled
    ? ""
    : isKnownInstalled
      ? t("pwa.helpInstalled")
      : isIOS
        ? t("pwa.helpIOS")
        : isMacSafari
          ? t("pwa.helpMacSafari")
          : isDesktopChromium && isInstallCapable
            ? t("pwa.helpDesktopChromium")
            : isAndroidChrome && !deferredInstallPrompt
              ? t("pwa.helpAndroidChrome")
              : deferredInstallPrompt
                ? t("pwa.helpDeferred")
                : t("pwa.helpUnavailable");
  const installProbePending = pwaInstallService.expectsPrompt() && !installProbeDone && !isKnownInstalled && !isNativeApp;
  const shouldShowInstallButton = !installProbePending && !isNativeApp && !isKnownInstalled && !(isAndroidChrome && !deferredInstallPrompt);
  const shouldShowInstallHelp = !installProbePending && !isNativeApp && !!installHelp &&
    (isKnownInstalled || isIOS || isMacSafari || isDesktopChromium || deferredInstallPrompt !== null || (isAndroidChrome && !deferredInstallPrompt));
  const handleInstall = useCallback(async () => {
    if (isKnownInstalled) return;
    if (deferredInstallPrompt) {
      await deferredInstallPrompt.prompt();
      try {
        if ((await deferredInstallPrompt.userChoice).outcome === "accepted") pwaInstallService.markInstalled();
      } finally {
        pwaInstallService.consumeDeferredPrompt();
      }
      return;
    }
    if (typeof window === "undefined") return;
    if (isIOS) return alertDialog(t("pwa.alertIOS"));
    if (isMacSafari) return alertDialog(t("pwa.alertMacSafari"));
    if (isDesktopChromium) return alertDialog(t("pwa.alertDesktopChromium"));
    if (isAndroidChrome) return alertDialog(t("pwa.alertAndroidChrome"));
    alertDialog(t("pwa.alertUnavailable"));
  }, [deferredInstallPrompt, isAndroidChrome, isDesktopChromium, isIOS, isKnownInstalled, isMacSafari, t]);

  return { isNativeApp, installLabel, installHelp, shouldShowInstallButton, shouldShowInstallHelp, handleInstall };
}
