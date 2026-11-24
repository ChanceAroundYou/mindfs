// 页面在场心跳。
//
// Web Push 是**唯一**与页面生命周期无关的通道：订阅一旦建立就永久有效，服务端每条
// 通知都会扇出到表里所有订阅。于是页面全关掉以后，后台 agent 每跑完一轮仍会弹桌面
// 通知 —— 用户看到的就是「页面关了通知还在蹦」。
//
// 这里给服务端补上它本来拿不到的事实：还有没有页面开着。
//   - 心跳：页面开着时每 30s 一次（后台标签被浏览器节流到约 1 次/分钟，仍在 TTL 内）；
//   - 关页：`pagehide` 用 sendBeacon 立即上报，正常关闭不必等 TTL 过期；
//   - 恢复：`pageshow`（bfcache 返回）与 `visibilitychange` 回前台时立刻补一次心跳。
//
// beacon 丢失（强杀、断电、进程崩溃）由服务端 TTL 兜底，service worker 也会在
// 第一次推送时发现「零个窗口」并自愈上报（见 vite.config.ts 的 push 处理器）。
import { controlPath } from "./net/controlPlane";
import { protectedJSON } from "./net/api";

/** 心跳间隔。必须显著小于服务端 TTL（10 分钟），容得下被节流与偶发失败。 */
export const PRESENCE_HEARTBEAT_MS = 30_000;

const CLIENT_ID_STORAGE_KEY = "mindfs.webpush.presenceClientId";

let cachedClientID = "";

function randomClientID(): string {
  try {
    if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
      return crypto.randomUUID();
    }
  } catch {
    // 老浏览器/非安全上下文没有 randomUUID，落到下面的随机串。
  }
  return `c-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

/**
 * 本标签页的稳定标识。放 sessionStorage：同一个浏览器开多个标签页各有各的 id，
 * 关掉一个只注销它自己，不会把还开着的另一个标签页一起判死。
 */
export function presenceClientID(): string {
  if (cachedClientID) {
    return cachedClientID;
  }
  try {
    const stored = window.sessionStorage.getItem(CLIENT_ID_STORAGE_KEY);
    if (stored) {
      cachedClientID = stored;
      return cachedClientID;
    }
  } catch {
    // 隐私模式下 sessionStorage 可能抛错，退化成进程内 id。
  }
  cachedClientID = randomClientID();
  try {
    window.sessionStorage.setItem(CLIENT_ID_STORAGE_KEY, cachedClientID);
  } catch {
    // 同上：存不下也不影响心跳本身。
  }
  return cachedClientID;
}

export function webPushPresenceURL(): string {
  return controlPath("/api/web-push/presence");
}

async function reportTouch(endpoint: string): Promise<void> {
  await protectedJSON(webPushPresenceURL(), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      endpoint,
      client_id: presenceClientID(),
      leave: false,
    }),
  });
}

function reportLeaveBeacon(endpoint: string): void {
  const url = webPushPresenceURL();
  const body = JSON.stringify({
    endpoint,
    client_id: presenceClientID(),
    leave: true,
  });
  try {
    if (typeof navigator !== "undefined" && typeof navigator.sendBeacon === "function") {
      // text/plain 是 CORS 安全清单内的类型；`application/json` 在跨源部署下会触发
      // 预检，而 beacon 不等响应，预检失败等于丢包。服务端只解析 JSON 正文，不看类型。
      const blob = new Blob([body], { type: "text/plain;charset=UTF-8" });
      if (navigator.sendBeacon(url, blob)) {
        return;
      }
    }
  } catch {
    // sendBeacon 被禁用时落到 keepalive fetch。
  }
  void fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body,
    keepalive: true,
  }).catch(() => undefined);
}

let stopActiveSession: (() => void) | null = null;

/** 停掉当前心跳（退订、切换节点、页面卸载之外的主动关闭）。 */
export function stopWebPushPresence(): void {
  if (stopActiveSession) {
    stopActiveSession();
    stopActiveSession = null;
  }
}

/**
 * 开始为一个订阅上报在场。同一时刻只保留一个心跳循环：重复调用会替换旧的
 * （例如 VAPID 更换后重新订阅，端点已经不同）。
 */
export function startWebPushPresence(endpoint: string): void {
  stopWebPushPresence();
  if (!endpoint) {
    return;
  }
  let stopped = false;
  const touch = () => {
    if (stopped) {
      return;
    }
    void reportTouch(endpoint).catch(() => undefined);
  };

  const timer = window.setInterval(touch, PRESENCE_HEARTBEAT_MS);
  const onVisibilityChange = () => {
    // 后台超过 TTL 的标签页回到前台时，先补一次心跳再让服务端恢复投递。
    if (document.visibilityState === "visible") {
      touch();
    }
  };
  const onPageShow = (event: PageTransitionEvent) => {
    // 从 bfcache 恢复：离开时发过 leave，回来要立刻重新登记。
    if (event.persisted) {
      touch();
    }
  };
  const onPageHide = () => {
    reportLeaveBeacon(endpoint);
  };

  document.addEventListener("visibilitychange", onVisibilityChange);
  window.addEventListener("pageshow", onPageShow);
  window.addEventListener("pagehide", onPageHide);
  touch();

  stopActiveSession = () => {
    stopped = true;
    window.clearInterval(timer);
    document.removeEventListener("visibilitychange", onVisibilityChange);
    window.removeEventListener("pageshow", onPageShow);
    window.removeEventListener("pagehide", onPageHide);
  };
}
