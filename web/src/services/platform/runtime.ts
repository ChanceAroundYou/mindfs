import { getStoredApiBaseURL, getStoredWsBaseURL } from "../prefs/storage";
import { getActiveNode, getNodeById, nodeRouteBase } from "../net/nodeRegistry";
import { isSelfNodeConnection } from "../../shared/selfNodeStore";
import { deriveLocalNodeBase, normalizeExplicitNodeBase } from "../net/nodeBase";
import { DEPLOY_PREFIX } from "../net/prefix";

// 原生壳的平台。"native" 是「确定在壳里、但认不出具体平台」的兜底。
// 曾经还有 "harmony"：鸿蒙 ArkTS 壳（上游带来的），已整体删除（AGENTS.md 的 fork 定制清单）。
export type NativePlatform = "web" | "android" | "ios" | "native";

type NativeRuntimeWindow = Window & {
  __MIND_FS_NATIVE_PLATFORM__?: string;
  __MIND_FS_DESKTOP_SHELL__?: boolean;
  chrome?: { webview?: unknown };
  Capacitor?: {
    getPlatform?: () => string;
    isNativePlatform?: () => boolean;
  };
  MindFSNative?: {
    platform?: string;
  };
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
  if (envPlatform === "android" || envPlatform === "ios") {
    return envPlatform;
  }

  const win = window as NativeRuntimeWindow;
  const injectedPlatform = String(win.__MIND_FS_NATIVE_PLATFORM__ || "").toLowerCase();
  if (injectedPlatform === "android" || injectedPlatform === "ios") {
    return injectedPlatform;
  }
  const bridgePlatform = String(win.MindFSNative?.platform || "").toLowerCase();
  if (bridgePlatform === "android" || bridgePlatform === "ios") {
    return bridgePlatform;
  }
  if (isCapacitorRuntime()) {
    const capacitorPlatform = String(win.Capacitor?.getPlatform?.() || "").toLowerCase();
    if (capacitorPlatform === "android") return "android";
    // Capacitor 在 iOS 上回报 "ios"。认不出来时仍按「在壳里」处理（原生桥接走同一套判定）。
    return capacitorPlatform === "ios" ? "ios" : "native";
  }
  return "web";
}

export function isNativeShellRuntime(): boolean {
  return getNativePlatform() !== "web";
}

/**
 * 桌面套壳（Windows 的 WebView2 窗口）里的页面。
 *
 * 它**不是**原生壳：没有 `MindFSNative` 桥，不能走原生下载/拉起/返回键那条路，
 * 所以不能并进 `isNativeShellRuntime()`。但页面里那些「把这个网页装成应用」的
 * 入口对它同样没有意义 —— 程序本来就是安装器装的，WebView2 也不支持 PWA 安装。
 *
 * 两条判据取并集：
 *   - 套壳在文档创建前注入的 `__MIND_FS_DESKTOP_SHELL__`（见 desktopShellInitScript）；
 *   - WebView2 宿主自带的 `window.chrome.webview`。旧版套壳二进制没有注入，
 *     但页面照样能借此认出自己在窗口里 —— 前端不必等套壳一起升级。
 * 普通浏览器标签页（Edge / Chrome）没有 `chrome.webview`。
 */
export function isDesktopShellRuntime(): boolean {
  if (!isBrowserRuntime()) {
    return false;
  }
  const w = window as NativeRuntimeWindow;
  if (w.__MIND_FS_DESKTOP_SHELL__ === true) {
    return true;
  }
  return Boolean(w.chrome?.webview);
}

export function isAndroidRuntime(): boolean {
  return getNativePlatform() === "android";
}

export function isIOSRuntime(): boolean {
  return getNativePlatform() === "ios";
}

// 安卓壳里那套**自定义** Capacitor 插件（NativeDownload / ReplyPoller / LauncherNodeSync /
// NativeCacheControl）只在 web/android 的 Java 侧实现，iOS 壳刻意没有对应 Swift 插件
// —— 判据与代价写在 docs/ios-shell.md（免费自签装机 + 无后台轮询）。调它们只会得到「插件未注册」的报错，
// 所以这些调用点在 iOS 上先短路，改用官方插件（Filesystem / Browser / Clipboard / App）。
// 注意：这不影响 window.MindFSNative 统一桥（getNativeBridge）—— 将来真加了 Swift 桥也不该被这里挡掉。
export function supportsCustomNativePlugins(): boolean {
  return !isIOSRuntime();
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

// 本机连接的寻址固定走页面所在的这台设备，而不是表里存的直连网址。
//
// 统一后的 pc 行在共享表里保留的是用户配置的 /mindfs-win 直连网址，但请求没有理由
// 绕一圈公网/反代：公网链路挂了（WSL 停机时整个 pc 入口不可达）本机操作照样要能用。
// 原生壳 origin 不可直接 fetch 时返回空串，调用方会退回记录里的地址。
function selfLocalBase(node: { id?: string; instanceId?: string } | null): string {
  if (!node || !isSelfNodeConnection(node)) return "";
  return deriveOriginBaseURL();
}

function resolveNodeBaseURL(nodeId?: string): string {
  if (nodeId) {
    const node = getNodeById(nodeId);
    // 直连优先、relay 兜底：一条节点记录可能只有 relayUrl（未配直连的边控端，
    // 或由主控经隧道发布）。只认 url 会把这类节点当成「没有地址」。
    if (node) {
      const local = selfLocalBase(node);
      if (local) return local;
      const base = nodeRouteBase(node);
      if (base) return normalizeExplicitNodeBase(base);
    }
    // 显式 nodeId 解析失败（节点被删/重加后旧 id、注册表未同步完成）时不回退 active node：
    // 静默回退会把请求发给错误节点（实测：其它节点会话的窗口拉取/关联文件 diff 串到当前节点）。
    // 返回空串让调用链落到“当前连接服务器”（origin/原生代理路径），与 UI 连接上下文一致。
    console.warn("[node-routing] explicit nodeId unresolvable, falling back to connected server", { nodeId });
    return "";
  }
  const active = getActiveNode();
  const activeLocal = selfLocalBase(active);
  if (activeLocal) return normalizeExplicitNodeBase(activeLocal);
  const activeBase = nodeRouteBase(active);
  if (activeBase) return normalizeExplicitNodeBase(activeBase);
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
      u = normalizeExplicitNodeBase(selfLocalBase(node) || nodeRouteBase(node));
    } else {
      const active = getActiveNode();
      u = normalizeExplicitNodeBase(selfLocalBase(active) || nodeRouteBase(active));
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
  // 套壳里既没有可安装的浏览器上下文，程序也已经装好了：连 beforeinstallprompt
  // 的监听都不必挂（WebView2 不会派发）。
  return !isNativeShellRuntime() && !isDesktopShellRuntime();
}
