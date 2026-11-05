import { registerPlugin } from "@capacitor/core";
import { appPath, appURL } from "./base";
import { getNodes } from "./nodeRegistry";
import { getNativeBridge } from "../platform/nativeBridge";
import { isHarmonyRuntime, isNativeShellRuntime } from "../platform/runtime";

type ReplyPollerPlugin = {
  configure(options: {
    apiBaseUrl?: string;
    token?: string;
    nodes?: Array<{ id: string; url: string }>;
    viewing?: string;
  }): Promise<void>;
  dismiss(options: { nodeId: string; rootId: string; sessionKey: string }): Promise<void>;
  testAlert(): Promise<{ error?: string; diag?: string }>;
  debugStatus(): Promise<{ debug?: string }>;
};

type NativeReplyPollerBridge = {
  configure?: (payload: string) => void;
  dismiss?: (nodeId: string, rootId: string, sessionKey: string) => void;
  takePendingSession?: () => string;
};

type NativeReplyPollerSyncWindow = Window & {
  MindFSReplyPoller?: NativeReplyPollerBridge;
  __mindfsLatestReplyPollerConfig?: ReplyPollerConfigPayload;
  __mindfsPendingReplySession?: { rootId: string; sessionKey: string };
};

const ReplyPoller = registerPlugin<ReplyPollerPlugin>("ReplyPoller");

type ReplyPollerConfigPayload = {
  apiBaseUrl: string;
};

let viewingKey = "";
let lastViewingKey = "";
let syncTimer: number | null = null;
let listenersBound = false;

/** 用户当前正在看的会话（nodeId␟rootId␟sessionKey）；原生靠它抑制「你正在看的这条」。 */
export function setNativeReplyViewing(nodeId: string, rootId: string, sessionKey: string): void {
  lastViewingKey = nodeId || rootId || sessionKey
    ? `${nodeId}\u001f${rootId}\u001f${sessionKey}`
    : "";
  viewingKey = isPageHidden() ? "" : lastViewingKey;
  scheduleNativeReplyPollerSync();
}

/** 主区切走（文件/看板/工作台）时调用：不再是「正在看这条会话」，但记住它以便切回来恢复。 */
export function clearNativeReplyViewing(): void {
  viewingKey = "";
  scheduleNativeReplyPollerSync();
}

/** 主区切回对话时调用。 */
export function restoreNativeReplyViewing(): void {
  viewingKey = isPageHidden() ? "" : lastViewingKey;
  scheduleNativeReplyPollerSync();
}

function isPageHidden(): boolean {
  return typeof document !== "undefined" && document.visibilityState === "hidden";
}

function scheduleNativeReplyPollerSync(): void {
  if (typeof window === "undefined") {
    return;
  }
  if (syncTimer !== null) {
    window.clearTimeout(syncTimer);
  }
  syncTimer = window.setTimeout(() => {
    syncTimer = null;
    void syncNativeReplyPoller();
  }, 300);
}

function absoluteNodeURL(path: string, nodeId: string): string {
  if (typeof window === "undefined") {
    return "";
  }
  try {
    return new URL(appPath(path, nodeId), window.location.href).toString();
  } catch {
    return "";
  }
}

export async function syncNativeReplyPoller(): Promise<void> {
  const native = getNativeBridge();
  const bridge = (window as NativeReplyPollerSyncWindow).MindFSReplyPoller;
  const hasHarmonyBridge = typeof native?.configureReplyPoller === "function" || typeof bridge?.configure === "function";
  if (!isNativeShellRuntime() && !hasHarmonyBridge) {
    return;
  }
  bindNativeReplyPollerListeners();
  const apiBaseUrl = nativeReplyPollerBaseURL();
  // 每个节点的完整 URL 由这里算（节点路由、user= 账户参数、挂载前缀都在 appPath 里）。
  // 原生只负责 GET，不许自己拼 —— 它只拿得到 origin，拼出来就是 404。
  const nodes = getNodes()
    .map((node) => String((node as { id?: string })?.id || "").trim())
    .filter(Boolean)
    .map((id) => ({ id, url: absoluteNodeURL("/api/replying-sessions", id) }))
    .filter((node) => /^https?:\/\//i.test(node.url));
  if (!nodes.length && !/^https?:\/\//i.test(apiBaseUrl)) {
    return;
  }
  const payload = {
    apiBaseUrl: nodes.length ? "" : apiBaseUrl,
    nodes,
    viewing: viewingKey,
  };
  rememberLatestReplyPollerConfig({ apiBaseUrl: apiBaseUrl || nodes[0]?.url || "" });
  if (typeof native?.configureReplyPoller === "function") {
    await native.configureReplyPoller(JSON.stringify(payload));
    return;
  }
  if (typeof bridge?.configure === "function") {
    bridge.configure(JSON.stringify(payload));
    return;
  }
  await ReplyPoller.configure(payload);
}

function bindNativeReplyPollerListeners(): void {
  if (listenersBound || typeof window === "undefined") {
    return;
  }
  listenersBound = true;
  document.addEventListener("visibilitychange", () => {
    viewingKey = isPageHidden() ? "" : lastViewingKey;
    scheduleNativeReplyPollerSync();
  });
  window.addEventListener("mindfs:nodes-changed", () => scheduleNativeReplyPollerSync());
}

function rememberLatestReplyPollerConfig(payload: ReplyPollerConfigPayload): void {
  if (typeof window === "undefined") {
    return;
  }
  (window as NativeReplyPollerSyncWindow).__mindfsLatestReplyPollerConfig = payload;
}

// 单节点兜底地址（节点表为空时用）：只到 origin + 挂载前缀，不带查询串。
function nativeReplyPollerBaseURL(): string {
  const direct = appURL("/");
  if (typeof window === "undefined") {
    return /^https?:\/\//i.test(direct) ? stripQueryAndSlash(direct) : "";
  }
  try {
    const resolved = new URL(direct, window.location.href);
    if (/^https?:$/i.test(resolved.protocol)) {
      return `${resolved.protocol}//${resolved.host}${resolved.pathname.replace(/\/+$/, "")}`;
    }
  } catch {
    // Fall through to origin-only fallback.
  }
  const origin = window.location.origin || "";
  if (/^https?:\/\//i.test(origin)) {
    return origin.replace(/\/+$/, "");
  }
  return "";
}

function stripQueryAndSlash(url: string): string {
  return url.split(/[?#]/)[0].replace(/\/+$/, "");
}



export async function dismissNativeReplyNotification(nodeId: string, rootId: string, sessionKey: string): Promise<void> {
  const native = getNativeBridge();
  const bridge = (window as NativeReplyPollerSyncWindow).MindFSReplyPoller;
  try {
    if (typeof native?.dismissReplyNotification === "function") {
      await native.dismissReplyNotification({ nodeId, rootId, sessionKey });
      return;
    }
    if (typeof bridge?.dismiss === "function") {
      bridge.dismiss(nodeId, rootId, sessionKey);
      return;
    }
    await ReplyPoller.dismiss({ nodeId, rootId, sessionKey });
  } catch {
    // ignore
  }
}

export function takePendingReplySession(): { nodeId?: string; rootId: string; sessionKey: string } | null {
  const bridge = (window as NativeReplyPollerSyncWindow).MindFSReplyPoller;
  try {
    let raw = "";
    if (typeof bridge?.takePendingSession === "function") {
      raw = bridge.takePendingSession() || "";
    }
    if (!raw) {
      const pending = (window as NativeReplyPollerSyncWindow).__mindfsPendingReplySession;
      if (pending && pending.rootId && pending.sessionKey) {
        (window as NativeReplyPollerSyncWindow).__mindfsPendingReplySession = undefined;
        return { nodeId: "", rootId: pending.rootId, sessionKey: pending.sessionKey };
      }
      return null;
    }
    const parsed = JSON.parse(raw);
    if (parsed && parsed.rootId && parsed.sessionKey) {
      return {
        nodeId: parsed.nodeId ? String(parsed.nodeId) : "",
        rootId: String(parsed.rootId),
        sessionKey: String(parsed.sessionKey),
      };
    }
  } catch {
    // ignore
  }
  return null;
}


// 返回给用户看的整段结论：投递结果（空 = 已发出）+ 最近一次后台轮询的实况。
// 「没有提醒」的三种成因（服务没跑 / 地址打错 / 接口报错）只有轮询记录能区分开。
export async function testNativeReplyAlert(): Promise<string> {
  let head = "测试提醒已发送";
  let diag = "";
  try {
    const result = await ReplyPoller.testAlert();
    if (result?.error) head = result.error;
    diag = result?.diag || "";
  } catch (ex) {
    head = ex instanceof Error ? ex.message : "测试提醒调用失败";
  }
  let poll = "";
  try {
    const result = await ReplyPoller.debugStatus();
    poll = describePollDebug(result?.debug || "");
  } catch {
    // 老版本 App 没有 debugStatus，只显示投递结果。
  }
  return [head, diag, poll].filter(Boolean).join("\n");
}

function describePollDebug(raw: string): string {
  if (!raw) {
    return "轮询：还没有记录 —— 后台服务没启动过（把 App 切到后台才会开始轮询）";
  }
  try {
    const data = JSON.parse(raw) as {
      ok?: boolean; sessions?: number; nodes?: number; apiBaseUrl?: string; error?: string; at?: string;
    };
    const state = data.ok ? `成功，${data.sessions ?? 0} 个会话` : `失败：${data.error || "未知错误"}`;
    const nodes = data.nodes ?? 0;
    const scope = data.apiBaseUrl
      ? `${nodes || 1} 个节点 · ${data.apiBaseUrl}`
      : `${nodes} 个节点`;
    return `轮询：${state} · ${scope} · ${data.at || ""}`;
  } catch {
    return raw;
  }
}
