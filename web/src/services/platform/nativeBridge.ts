import type { LauncherNode } from "../prefs/storage";

export type NativeAppInfo = {
  version?: string;
  build?: string;
};

export type NativeDownloadResult = {
  downloadId?: number | string;
  filename?: string;
  directory?: string;
  path?: string;
};

export type NativeReplyPollerConfig = {
  apiBaseUrl: string;
  token?: string;
};

export type NativeBridge = {
  platform?: "android" | "ios" | string;
  download?: (input: { url: string; filename: string } | string) => Promise<NativeDownloadResult> | NativeDownloadResult | string | void;
  saveBase64?: (input: { dataBase64: string; filename: string; mimeType?: string } | string) => Promise<NativeDownloadResult> | NativeDownloadResult | string | void;
  openExternalURL?: (url: string) => Promise<string | void> | string | void;
  getAppInfo?: () => Promise<NativeAppInfo | string> | NativeAppInfo | string;
  configureReplyPoller?: (input: NativeReplyPollerConfig | string) => Promise<void> | void;
  dismissReplyNotification?: (input: { nodeId: string; rootId: string; sessionKey: string } | string) => Promise<void> | void;
  markClearWebViewCacheOnNextLaunch?: () => Promise<{ scheduled?: boolean } | void> | { scheduled?: boolean } | void;
  clearPendingWebViewCacheClear?: () => Promise<{ scheduled?: boolean } | void> | { scheduled?: boolean } | void;
  getLauncherNodes?: () => Promise<{ nodes?: LauncherNode[]; count?: number } | LauncherNode[] | string> | { nodes?: LauncherNode[]; count?: number } | LauncherNode[] | string;
  setLauncherNodes?: (input: { nodes: LauncherNode[] } | string) => Promise<{ stored?: boolean; count?: number } | void> | { stored?: boolean; count?: number } | void;
  writeClipboardText?: (text: string) => Promise<boolean | void> | boolean | void;
};

// 原生壳的桥对象形状（window.MindFSNative）。
// 曾经还有两个成员/字段属于上游带来的东西，已随 relay 与鸿蒙壳一起删掉：
//   · consumeRelayNodes —— relay 节点同步的取件口，relay 整体删除后没有任何调用方
//   · MindFSHarmony      —— 鸿蒙 ArkTS 壳注入的第二个桥对象
type NativeBridgeWindow = Window & {
  MindFSNative?: NativeBridge;
};

export function getNativeBridge(): NativeBridge | null {
  if (typeof window === "undefined") {
    return null;
  }
  const win = window as NativeBridgeWindow;
  const bridge = win.MindFSNative;
  return bridge && hasCallableNativeMethod(bridge) ? bridge : null;
}

function hasCallableNativeMethod(bridge: NativeBridge): boolean {
  return [
    bridge.download,
    bridge.saveBase64,
    bridge.openExternalURL,
    bridge.getAppInfo,
    bridge.configureReplyPoller,
    bridge.getLauncherNodes,
    bridge.setLauncherNodes,
    bridge.writeClipboardText,
  ].some((method) => typeof method === "function");
}

export function parseNativeJSON<T>(value: unknown, fallback: T): T {
  if (typeof value !== "string") {
    return (value ?? fallback) as T;
  }
  const trimmed = value.trim();
  if (!trimmed) {
    return fallback;
  }
  try {
    return JSON.parse(trimmed) as T;
  } catch {
    return fallback;
  }
}
