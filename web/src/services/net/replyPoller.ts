import { registerPlugin } from "@capacitor/core";
import { appURL } from "./base";
import { getNativeBridge } from "../platform/nativeBridge";
import { isHarmonyRuntime, isNativeShellRuntime } from "../platform/runtime";

type ReplyPollerPlugin = {
  configure(options: {
    apiBaseUrl: string;
    token?: string;
  }): Promise<void>;
  dismiss(options: { rootId: string; sessionKey: string }): Promise<void>;
  testAlert(): Promise<{ error?: string }>;
};

type NativeReplyPollerBridge = {
  configure?: (payload: string) => void;
  dismiss?: (rootId: string, sessionKey: string) => void;
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

export async function syncNativeReplyPoller(): Promise<void> {
  const native = getNativeBridge();
  const bridge = (window as NativeReplyPollerSyncWindow).MindFSReplyPoller;
  const hasHarmonyBridge = typeof native?.configureReplyPoller === "function" || typeof bridge?.configure === "function";
  if (!isNativeShellRuntime() && !hasHarmonyBridge) {
    return;
  }
  const apiBaseUrl = nativeReplyPollerBaseURL();
  if (!/^https?:\/\//i.test(apiBaseUrl) || isLocalShellURL(apiBaseUrl)) {
    return;
  }
  const payload = {
    apiBaseUrl,
  };
  rememberLatestReplyPollerConfig(payload);
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

function rememberLatestReplyPollerConfig(payload: ReplyPollerConfigPayload): void {
  if (typeof window === "undefined") {
    return;
  }
  (window as NativeReplyPollerSyncWindow).__mindfsLatestReplyPollerConfig = payload;
}

function nativeReplyPollerBaseURL(): string {
  const direct = appURL("/");
  if (/^https?:\/\//i.test(direct)) {
    return direct.replace(/\/+$/, "");
  }
  if (typeof window === "undefined") {
    return "";
  }
  try {
    const resolved = new URL(direct, window.location.href);
    if (/^https?:$/i.test(resolved.protocol)) {
      return resolved.href.replace(/\/+$/, "");
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

function isLocalShellURL(value: string): boolean {
  try {
    const url = new URL(value);
    if (isHarmonyRuntime()) {
      return url.hostname === "mindfs.local";
    }
    return url.protocol === "http:" && (url.hostname === "localhost" || url.hostname === "127.0.0.1" || url.hostname === "[::1]");
  } catch {
    return false;
  }
}


export async function dismissNativeReplyNotification(rootId: string, sessionKey: string): Promise<void> {
  const native = getNativeBridge();
  const bridge = (window as NativeReplyPollerSyncWindow).MindFSReplyPoller;
  try {
    if (typeof native?.dismissReplyNotification === "function") {
      await native.dismissReplyNotification({ rootId, sessionKey });
      return;
    }
    if (typeof bridge?.dismiss === "function") {
      bridge.dismiss(rootId, sessionKey);
      return;
    }
    await ReplyPoller.dismiss({ rootId, sessionKey });
  } catch {
    // ignore
  }
}

export function takePendingReplySession(): { rootId: string; sessionKey: string } | null {
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
        return { rootId: pending.rootId, sessionKey: pending.sessionKey };
      }
      return null;
    }
    const parsed = JSON.parse(raw);
    if (parsed && parsed.rootId && parsed.sessionKey) {
      return { rootId: String(parsed.rootId), sessionKey: String(parsed.sessionKey) };
    }
  } catch {
    // ignore
  }
  return null;
}


// 返回空串 = 已投递；非空 = 系统侧拦截原因（权限、渠道被关、投递异常），必须显示给用户。
export async function testNativeReplyAlert(): Promise<string> {
  try {
    const result = await ReplyPoller.testAlert();
    return result?.error || "";
  } catch (ex) {
    return ex instanceof Error ? ex.message : "测试提醒调用失败";
  }
}
