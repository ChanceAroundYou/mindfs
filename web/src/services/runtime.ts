import { getStoredApiBaseURL, getStoredWsBaseURL } from "./storage";
import { getActiveNode, getNodeById } from "./nodeRegistry";

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

function sanitizeBaseURL(value: string | null | undefined): string {
  if (!value) {
    return "";
  }
  return value.trim().replace(/\/+$/, "");
}

function readMeta(name: string): string {
  if (typeof document === "undefined") {
    return "";
  }
  const node = document.querySelector<HTMLMetaElement>(`meta[name="${name}"]`);
  return sanitizeBaseURL(node?.content);
}

function readStorage(key: string): string {
  if (key === "mindfs_api_base_url") {
    return sanitizeBaseURL(getStoredApiBaseURL());
  }
  if (key === "mindfs_ws_base_url") {
    return sanitizeBaseURL(getStoredWsBaseURL());
  }
  if (!isBrowserRuntime()) {
    return "";
  }
  try {
    return sanitizeBaseURL(window.localStorage.getItem(key));
  } catch {
    return "";
  }
}

// 反代子路径前缀：当前页面本身挂在子路径下（如 /mindfs）时，节点 URL 若是裸 origin，
// 应补上该前缀，请求才落到 host.domain/mindfs/api/...。
// 例：页面在 https://home.xiaokubao.space/mindfs/ 加载 → prefix = "/mindfs"。
function detectReverseProxyPrefix(): string {
  if (!isBrowserRuntime()) {
    return "";
  }
  const pathname = window.location.pathname || "/";
  const segments = pathname.split("/").filter(Boolean);
  if (segments.length === 0) {
    return "";
  }
  // 取第一段作为反代前缀（/mindfs/... → /mindfs）
  const prefix = `/${segments[0]}`;
  return prefix;
}

function parseOriginHost(input: string): { origin: string; path: string } {
  try {
    const u = new URL(input);
    let path = u.pathname.replace(/\/+$/, "");
    if (path === "") path = "";
    return { origin: u.origin, path };
  } catch {
    return { origin: input, path: "" };
  }
}

// 将节点/基础 URL 统一到反代子路径：若它是裸 origin（无路径），但当前页面有反代前缀，
// 则补上前缀，保证 host/xxx/api 而不再 404 为 host/api。
export function normalizeBaseURLWithPrefix(input: string): string {
  const base = sanitizeBaseURL(input);
  if (!base) return "";
  const { path } = parseOriginHost(base);
  if (path) {
    return base;
  }
  const prefix = detectReverseProxyPrefix();
  if (!prefix) {
    return base;
  }
  return `${base.replace(/\/+$/, "")}${prefix}`;
}

function deriveOriginBaseURL(): string {
  if (!isBrowserRuntime()) {
    return "";
  }
  return normalizeBaseURLWithPrefix(sanitizeBaseURL(window.location.origin));
}

function resolveNodeBaseURL(nodeId?: string): string {
  if (nodeId) {
    const node = getNodeById(nodeId);
    if (node?.url) return normalizeBaseURLWithPrefix(node.url);
  }
  const active = getActiveNode();
  if (active?.url) return normalizeBaseURLWithPrefix(active.url);
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
      if (node?.url) u = normalizeBaseURLWithPrefix(node.url);
    } else {
      const active = getActiveNode();
      if (active?.url) u = normalizeBaseURLWithPrefix(active.url);
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
