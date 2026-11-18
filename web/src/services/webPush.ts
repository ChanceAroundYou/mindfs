import { controlPath } from "./net/controlPlane";
import { protectedJSON } from "./net/api";
import { isNativeShellRuntime } from "./platform/runtime";
import { translateNow } from "../i18n";

export type WebPushStatus = {
  supported: boolean;
  enabled: boolean;
  subscribed: boolean;
  permission: NotificationPermission | "unsupported";
  reason?: string;
  endpoint?: string;
  endpoint_hint?: string;
  platform?: string;
  subscription_count?: number;
};

type ServerStatus = {
  enabled?: boolean;
  vapid_public_key?: string;
  subscription_count?: number;
  platform_counts?: Record<string, number>;
};

type PushSubscriptionJSON = {
  endpoint?: string;
  keys?: {
    auth?: string;
    p256dh?: string;
  };
};

const serviceWorkerReadyTimeoutMs = 8000;
const pushSubscribeTimeoutMs = 15000;

function hasNotificationAPI(): boolean {
  return typeof window !== "undefined" && "Notification" in window;
}

function isStandaloneDisplay(): boolean {
  if (typeof window === "undefined") {
    return false;
  }
  const nav = window.navigator as Navigator & { standalone?: boolean };
  return nav.standalone === true || window.matchMedia?.("(display-mode: standalone)")?.matches === true;
}

function isIOSLike(): boolean {
  if (typeof navigator === "undefined") {
    return false;
  }
  const ua = navigator.userAgent || "";
  const platform = navigator.platform || "";
  return /iPad|iPhone|iPod/.test(ua) || (platform === "MacIntel" && navigator.maxTouchPoints > 1);
}

function currentPlatform(): string {
  if (isIOSLike()) {
    return "ios-pwa";
  }
  if (isStandaloneDisplay()) {
    return "desktop-pwa";
  }
  return "web";
}

function endpointHint(endpoint: string): string {
  const value = String(endpoint || "").trim();
  if (!value) {
    return "";
  }
  try {
    const url = new URL(value);
    const tail = url.pathname.split("/").filter(Boolean).pop() || "";
    return tail ? `${url.hostname}/${tail.slice(-8)}` : url.hostname;
  } catch {
    return value.length > 12 ? value.slice(-12) : value;
  }
}

function capabilityReason(): string {
  if (isNativeShellRuntime()) {
    return "native_shell";
  }
  if (typeof window === "undefined" || typeof navigator === "undefined") {
    return "not_browser";
  }
  if (!window.isSecureContext) {
    return "insecure_context";
  }
  if (!("serviceWorker" in navigator) || !("PushManager" in window) || !hasNotificationAPI()) {
    return "unsupported";
  }
  if (isIOSLike() && !isStandaloneDisplay()) {
    return "ios_requires_home_screen";
  }
  return "";
}

function urlBase64ToArrayBuffer(value: string): ArrayBuffer {
  const padding = "=".repeat((4 - (value.length % 4)) % 4);
  const base64 = `${value}${padding}`.replace(/-/g, "+").replace(/_/g, "/");
  const raw = window.atob(base64);
  const buffer = new ArrayBuffer(raw.length);
  const out = new Uint8Array(buffer);
  for (let i = 0; i < raw.length; i += 1) {
    out[i] = raw.charCodeAt(i);
  }
  return buffer;
}

function arrayBufferEqual(left: ArrayBuffer | null | undefined, right: ArrayBuffer): boolean {
  if (!left || left.byteLength !== right.byteLength) {
    return false;
  }
  const leftBytes = new Uint8Array(left);
  const rightBytes = new Uint8Array(right);
  for (let i = 0; i < leftBytes.length; i += 1) {
    if (leftBytes[i] !== rightBytes[i]) {
      return false;
    }
  }
  return true;
}

async function withTimeout<T>(promise: Promise<T>, timeoutMs: number, message: string): Promise<T> {
  let timeoutID = 0;
  const timeout = new Promise<never>((_, reject) => {
    timeoutID = window.setTimeout(() => reject(new Error(message)), timeoutMs);
  });
  try {
    return await Promise.race([promise, timeout]);
  } finally {
    window.clearTimeout(timeoutID);
  }
}

async function saveSubscription(subscription: PushSubscription, platform: string): Promise<void> {
  const json = subscription.toJSON() as PushSubscriptionJSON;
  await protectedJSON(controlPath("/api/web-push/subscriptions"), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      endpoint: json.endpoint || subscription.endpoint,
      keys: json.keys,
      platform,
    }),
  });
}

async function fetchServerStatus(): Promise<ServerStatus> {
  return protectedJSON<ServerStatus>(controlPath("/api/web-push/status"));
}

function serviceWorkerURL(): URL {
  return new URL("service-worker.js", window.location.href);
}

async function ensureServiceWorkerRegistration(): Promise<ServiceWorkerRegistration> {
  if (!("serviceWorker" in navigator)) {
    throw new Error(translateNow("webPush.serviceWorkerUnsupported"));
  }
  const existing = await navigator.serviceWorker.getRegistration("./").catch(() => undefined);
  if (existing) {
    void existing.update().catch(() => undefined);
  } else {
    await navigator.serviceWorker.register(serviceWorkerURL(), { scope: "./" });
  }

  let timeoutID = 0;
  const timeout = new Promise<never>((_, reject) => {
    timeoutID = window.setTimeout(() => {
      reject(new Error(translateNow("webPush.serviceWorkerTimeout")));
    }, serviceWorkerReadyTimeoutMs);
  });
  try {
    return await Promise.race([navigator.serviceWorker.ready, timeout]);
  } finally {
    window.clearTimeout(timeoutID);
  }
}

async function currentSubscription(): Promise<PushSubscription | null> {
  const registration = await ensureServiceWorkerRegistration();
  return registration.pushManager.getSubscription();
}

export async function getWebPushStatus(): Promise<WebPushStatus> {
  const reason = capabilityReason();
  const permission = hasNotificationAPI() ? Notification.permission : "unsupported";
  if (reason) {
    return { supported: false, enabled: false, subscribed: false, permission, reason };
  }
  if (permission === "denied") {
    return { supported: true, enabled: false, subscribed: false, permission, reason: "permission_denied" };
  }
  const [server, subscription] = await Promise.all([
    fetchServerStatus().catch(() => ({} as ServerStatus)),
    currentSubscription().catch(() => null),
  ]);
  return {
    supported: true,
    enabled: Boolean(server.enabled && server.vapid_public_key),
    subscribed: Boolean(subscription),
    permission,
    reason: server.enabled ? "" : "server_disabled",
    endpoint: subscription?.endpoint || "",
    endpoint_hint: endpointHint(subscription?.endpoint || ""),
    platform: currentPlatform(),
    subscription_count: Number(server.subscription_count || 0),
  };
}

export async function subscribeWebPush(): Promise<WebPushStatus> {
  const reason = capabilityReason();
  if (reason) {
    return getWebPushStatus();
  }
  const server = await fetchServerStatus();
  if (!server.enabled || !server.vapid_public_key) {
    return getWebPushStatus();
  }
  const permission = await Notification.requestPermission();
  if (permission !== "granted") {
    return getWebPushStatus();
  }
  const registration = await ensureServiceWorkerRegistration();
  const applicationServerKey = urlBase64ToArrayBuffer(server.vapid_public_key);
  const existing = await registration.pushManager.getSubscription();
  if (existing && !arrayBufferEqual(existing.options?.applicationServerKey, applicationServerKey)) {
    await existing.unsubscribe().catch(() => false);
  }
  const subscription =
    existing && arrayBufferEqual(existing.options?.applicationServerKey, applicationServerKey)
      ? existing
      : await withTimeout(registration.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey,
        }),
        pushSubscribeTimeoutMs,
        translateNow("webPush.subscribeTimeout"),
      ).catch((error) => {
        const message = error instanceof Error ? error.message : String(error || "");
        if (/denied|notallowed|permission/i.test(message)) {
          throw new Error(isIOSLike()
            ? translateNow("webPush.permissionDeniedIOS")
            : translateNow("webPush.permissionDenied"));
        }
        throw error;
      });
  await saveSubscription(subscription, currentPlatform());
  return getWebPushStatus();
}

export async function unsubscribeWebPush(): Promise<WebPushStatus> {
  const subscription = await currentSubscription().catch(() => null);
  const endpoint = subscription?.endpoint || "";
  if (subscription) {
    await subscription.unsubscribe().catch(() => false);
  }
  if (endpoint) {
    await protectedJSON(controlPath("/api/web-push/subscriptions"), {
      method: "DELETE",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ endpoint }),
    }).catch(() => null);
  }
  return getWebPushStatus();
}

/**
 * 页面在后台时**唯一**还能送达的通道就是 Web Push：标签页被冻结后，5s 轮询
 * 与 WebAudio 都不再执行。这里负责让「已经授权过的浏览器」自动完成订阅 ——
 * 以前只有通知面板里手动点开关才会订阅，所以服务端订阅表长期是 0 条，
 * 后台提醒事实上从未生效。
 *
 * 不主动弹权限框（`prompt: false`，默认）：静默订阅只发生在权限已是 granted 时。
 * 需要弹框的场景由调用方在用户第一次真实交互后传 `prompt: true`。
 */
export async function ensureWebPushSubscribed(options: { prompt?: boolean } = {}): Promise<WebPushStatus> {
  if (capabilityReason()) {
    return getWebPushStatus();
  }
  if (Notification.permission === "denied") {
    return getWebPushStatus();
  }
  if (Notification.permission === "default") {
    if (!options.prompt) {
      return getWebPushStatus();
    }
    const permission = await Notification.requestPermission();
    if (permission !== "granted") {
      markBackgroundPushDeclined();
      return getWebPushStatus();
    }
  }
  const server = await fetchServerStatus().catch(() => ({} as ServerStatus));
  if (!server.enabled || !server.vapid_public_key) {
    return getWebPushStatus();
  }
  const registration = await ensureServiceWorkerRegistration();
  const applicationServerKey = urlBase64ToArrayBuffer(server.vapid_public_key);
  let existing = await registration.pushManager.getSubscription();
  if (existing && !arrayBufferEqual(existing.options?.applicationServerKey, applicationServerKey)) {
    // 换了节点（VAPID 公钥不同）时，旧订阅对当前服务端无效。
    await existing.unsubscribe().catch(() => false);
    existing = null;
  }
  const subscription =
    existing ??
    await withTimeout(
      registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey }),
      pushSubscribeTimeoutMs,
      translateNow("webPush.subscribeTimeout"),
    );
  // 每次启动都回传一次：服务端订阅表在换 config-dir / 重装后会丢，而 upsert 幂等。
  await saveSubscription(subscription, currentPlatform());
  return getWebPushStatus();
}

const backgroundPushDeclinedKey = "mindfs.webpush.autoDeclined";

/** 用户在系统权限框里点过「不允许」后不再反复弹；权限本身也改不回来。 */
export function backgroundPushDeclined(): boolean {
  try {
    return window.localStorage.getItem(backgroundPushDeclinedKey) === "1";
  } catch {
    return false;
  }
}

export function markBackgroundPushDeclined(): void {
  try {
    window.localStorage.setItem(backgroundPushDeclinedKey, "1");
  } catch {
    // localStorage 不可用（隐私模式）时只是会再问一次，不影响功能。
  }
}

/** 权限还是 default 时才值得等一次用户交互去弹框。 */
export function needsNotificationPermission(): boolean {
  return hasNotificationAPI() && Notification.permission === "default";
}

export async function sendWebPushTest(): Promise<void> {
  const subscription = await currentSubscription();
  const endpoint = subscription?.endpoint || "";
  if (!endpoint) {
    throw new Error(translateNow("webPush.noSubscription"));
  }
  await protectedJSON(controlPath("/api/web-push/test"), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ endpoint }),
  });
}

export function webPushReasonLabel(reason?: string): string {
  switch (reason) {
    case "ios_requires_home_screen":
      return translateNow("webPush.reason.iosHomeScreen");
    case "insecure_context":
      return translateNow("webPush.reason.insecureContext");
    case "server_disabled":
      return translateNow("webPush.reason.serverDisabled");
    case "permission_denied":
      return translateNow("webPush.reason.permissionDenied");
    case "native_shell":
      return translateNow("webPush.reason.nativeShell");
    case "unsupported":
      return translateNow("webPush.reason.unsupported");
    default:
      return "";
  }
}
