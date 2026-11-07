import { getNativeBridge } from "./nativeBridge";
import { isCapacitorRuntime, isNativeShellRuntime, supportsCustomNativePlugins } from "./runtime";

type ExternalBrowserBridge = {
  open?: (url: string) => string | void;
};

// 官方 @capacitor/browser：iOS 上模态弹一个自带关闭按钮的浏览器视图，安卓上开 Custom Tab。
// 用它是因为 iOS 壳刻意没有 MindFSExternalBrowser 桥（不写自定义 Swift 插件，见 docs/ios-shell.md），
// 而 WKWebView 的 window.open 在没有 WKUIDelegate 接管时会被直接忽略 —— 不接这条链，链接点了没反应。
async function openWithCapacitorBrowser(url: string): Promise<boolean> {
  if (!isCapacitorRuntime()) {
    return false;
  }
  try {
    const { Browser } = await import("@capacitor/browser");
    await Browser.open({ url });
    return true;
  } catch {
    return false;
  }
}

export function openExternalURL(url: string): void {
  if (typeof window === "undefined") {
    return;
  }
  if (isNativeShellRuntime()) {
    const native = getNativeBridge();
    if (typeof native?.openExternalURL === "function") {
      const result = native.openExternalURL(url);
      if (result instanceof Promise) {
        void result.catch((error) => {
          console.warn("[platform-navigation] native external browser failed", error);
          window.open(url, "_blank", "noopener,noreferrer");
        });
        return;
      }
      if (!result) {
        return;
      }
      console.warn("[platform-navigation] native external browser failed", result);
    }
    const bridge = (window as Window & {
      MindFSExternalBrowser?: ExternalBrowserBridge;
    }).MindFSExternalBrowser;
    if (typeof bridge?.open === "function") {
      const error = bridge.open(url);
      if (!error) {
        return;
      }
      console.warn("[platform-navigation] native external browser failed", error);
    }
    if (!supportsCustomNativePlugins()) {
      void openWithCapacitorBrowser(url).then((opened) => {
        if (!opened) {
          window.open(url, "_blank", "noopener,noreferrer");
        }
      });
      return;
    }
    window.open(url, "_blank", "noopener,noreferrer");
    return;
  }
  window.open(url, "_blank", "noopener,noreferrer");
}

export function replaceLocation(url: string): void {
  if (typeof window === "undefined") {
    return;
  }
  window.location.replace(url);
}
