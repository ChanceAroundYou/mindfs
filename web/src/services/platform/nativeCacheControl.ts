import { registerPlugin } from "@capacitor/core";
import { getNativeBridge } from "./nativeBridge";
import { isNativeShellRuntime, supportsCustomNativePlugins } from "./runtime";

type NativeCacheControlPlugin = {
  markClearWebViewCacheOnNextLaunch: () => Promise<{ scheduled: boolean }>;
  clearPendingWebViewCacheClear: () => Promise<{ scheduled: boolean }>;
};

const NativeCacheControl = registerPlugin<NativeCacheControlPlugin>(
  "NativeCacheControl",
);

export async function scheduleWebViewCacheClearOnNextLaunch(): Promise<void> {
  if (!isNativeShellRuntime() || !supportsCustomNativePlugins()) {
    // 这条链只为「壳自己装更新包」服务（装完下次启动清缓存）。iOS 没有自更新通道，
    // 也没有 NativeCacheControl 插件 —— 见 docs/ios-shell.md。
    return;
  }
  try {
    const native = getNativeBridge();
    if (typeof native?.markClearWebViewCacheOnNextLaunch === "function") {
      await native.markClearWebViewCacheOnNextLaunch();
      return;
    }
    await NativeCacheControl.markClearWebViewCacheOnNextLaunch();
  } catch (error) {
    console.warn("[native-cache-control] schedule failed", error);
  }
}

export async function cancelScheduledWebViewCacheClear(): Promise<void> {
  if (!isNativeShellRuntime() || !supportsCustomNativePlugins()) {
    return;
  }
  try {
    const native = getNativeBridge();
    if (typeof native?.clearPendingWebViewCacheClear === "function") {
      await native.clearPendingWebViewCacheClear();
      return;
    }
    await NativeCacheControl.clearPendingWebViewCacheClear();
  } catch (error) {
    console.warn("[native-cache-control] cancel failed", error);
  }
}
