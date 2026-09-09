import { getStoredApiBaseURL, getStoredWsBaseURL } from "./storage";
import { getActiveNode, getNodeById } from "./nodeRegistry";
import { deriveLocalNodeBase, normalizeExplicitNodeBase } from "./nodeBase";
import { DEPLOY_PREFIX } from "./prefix";

export type NativePlatform = "web" | "android" | "harmony" | "native";

type NativeRuntimeWindow = Window & {
  __MIND_FS_NATIVE_PLATFORM__?: string;
  Capacitor?: {
    getPlatform?: () => string;
    isNativePlatform?: () => boolean;
  };
  MindFSNative?: {
    platform?: string;
  };
  MindFSHarmony?: unknown;
};

export function isBrowserRuntime(): boolean {
  return typeof window !== "undefined";
}

export function isCapacitorRuntime(): boolean {
  if (!isBrowserRuntime()) {
    return false;
  }
  const win = window as Window & {
    Capacitor?: { isNativePlatform?: () => boolean; getPlatform?: () => string };
  };
  if (typeof win.Capacitor?.isNativePlatform === "function") {
    return win.Capacitor.isNativePlatform();
  }
  const protocol = window.location.protocol;
  return protocol === "capacitor:" || protocol === "ionic:";
}

export function getNativePlatform(): NativePlatform {
  if (!isBrowserRuntime()) {
    return "web";
  }
  const envPlatform = String(import.meta.env.VITE_NATIVE_PLATFORM || "").toLowerCase();
  if (envPlatform === "harmony" || envPlatform === "android") {
    return envPlatform;
  }

  const win = window as NativeRuntimeWindow;
  const injectedPlatform = String(win.__MIND_FS_NATIVE_PLATFORM__ || "").toLowerCase();
  if (injectedPlatform === "harmony" || injectedPlatform === "android") {
    return injectedPlatform;
  }
  const bridgePlatform = String(win.MindFSNative?.platform || "").toLowerCase();
  if (bridgePlatform === "harmony" || bridgePlatform === "android") {
    return bridgePlatform;
  }
  if (win.MindFSHarmony) {
    return "harmony";
  }
  if (isCapacitorRuntime()) {
    const capacitorPlatform = String(win.Capacitor?.getPlatform?.() || "").toLowerCase();
    return capacitorPlatform === "android" ? "android" : "native";
  }
  return "web";
}

export function isNativeShellRuntime(): boolean {
  return getNativePlatform() !== "web";
}

export function isAndroidRuntime(): boolean {
  return getNativePlatform() === "android";
}

export function isHarmonyRuntime(): boolean {
  return getNativePlatform() === "harmony";
}

function readMeta(name: string): string {
  if (typeof document === "undefined") {
    return "";
  }
  const node = document.querySelector<HTMLMetaElement>(`meta[name="${name}"]`);
  return normalizeExplicitNodeBase(node?.content || "");
}

function readStorage(key: string): string {
  if (key === "mindfs_api_base_url") {
    return normalizeExplicitNodeBase(getStoredApiBaseURL() || "");
  }
  if (key === "mindfs_ws_base_url") {
    return normalizeExplicitNodeBase(getStoredWsBaseURL() || "");
  }
  if (!isBrowserRuntime()) {
    return "";
  }
  try {
    return normalizeExplicitNodeBase(window.localStorage.getItem(key) || "");
  } catch {
    return "";
  }
}

function deriveOriginBaseURL(): string {
  if (!isBrowserRuntime()) {
    return "";
  }
  return deriveLocalNodeBase(window.location.origin, DEPLOY_PREFIX);
}

function resolveNodeBaseURL(nodeId?: string): string {
  if (nodeId) {
    const node = getNodeById(nodeId);
    if (node?.url) return normalizeExplicitNodeBase(node.url);
    // 显式 nodeId 解析失败（节点被删/重加后旧 id、注册表未同步完成）时不回退 active node：
    // 静默回退会把请求发给错误节点（实测：其它节点会话的窗口拉取/关联文件 diff 串到当前节点）。
    // 返回空串让调用链落到“当前连接服务器”（origin/原生代理路径），与 UI 连接上下文一致。
    console.warn("[node-routing] explicit nodeId unresolvable, falling back to connected server", { nodeId });
    return "";
  }
  const active = getActiveNode();
  if (active?.url) return normalizeExplicitNodeBase(active.url);
  return "";
}

export function getApiBaseURL(nodeId?: string): string {
  const nodeURL = resolveNodeBaseURL(nodeId);
  if (nodeURL) return nodeURL;
  const configured = readStorage("mindfs_api_base_url") || readMeta("mindfs-api-base-url");
  if (configured) {
    return configured;
  }
  if (isNativeShellRuntime()) {
    return "";
  }
  return deriveOriginBaseURL();
}

export function getWsBaseURL(nodeId?: string): string {
  // ws base derived from api base when no explicit ws storage
  const nodeWs = (() => {
    let u = "";
    if (nodeId) {
      const node = getNodeById(nodeId);
      if (node?.url) u = normalizeExplicitNodeBase(node.url);
    } else {
      const active = getActiveNode();
      if (active?.url) u = normalizeExplicitNodeBase(active.url);
    }
    if (u) {
      if (u.startsWith("https://")) return `wss://${u.slice("https://".length)}`;
      if (u.startsWith("http://")) return `ws://${u.slice("http://".length)}`;
      return u;
    }
    return "";
  })();
  if (nodeWs) return nodeWs;
  const configured = readStorage("mindfs_ws_base_url") || readMeta("mindfs-ws-base-url");
  if (configured) {
    return configured;
  }
  const apiBaseURL = getApiBaseURL(nodeId);
  if (!apiBaseURL) {
    return "";
  }
  if (apiBaseURL.startsWith("https://")) {
    return `wss://${apiBaseURL.slice("https://".length)}`;
  }
  if (apiBaseURL.startsWith("http://")) {
    return `ws://${apiBaseURL.slice("http://".length)}`;
  }
  return apiBaseURL;
}

export function shouldRegisterServiceWorker(): boolean {
  return !isNativeShellRuntime();
}

export function shouldEnablePWAInstall(): boolean {
  return !isNativeShellRuntime();
}
