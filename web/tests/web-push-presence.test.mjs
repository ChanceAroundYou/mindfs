// Web Push「页面关闭后不再弹通知」的契约。
//
// 用户报的现象：页面/界面已经关掉，多台设备上的桌面通知仍然不停往外蹦。
// 根因是 Web Push 与页面生命周期无关：订阅永久有效、服务端全量扇出、事件源是
// 后台 agent。修复引入「页面在场」这一事实：客户端心跳 + 关页 beacon + 服务端过滤，
// service worker 在零窗口时自愈上报。这里的源码级守卫钉住整条链路，防止
// 后续重构把任一环悄悄摘掉（摘掉后功能看起来一切正常，只是通知又开始乱弹）。
import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

const read = (relative) =>
  fs.readFileSync(path.resolve(import.meta.dirname, "..", relative), "utf8");

test("订阅上报声明 presence 能力，客户端从此受在场约束", async () => {
  const service = read("src/services/webPush.ts");
  const body = service.slice(
    service.indexOf("async function saveSubscription"),
    service.indexOf("async function fetchServerStatus"),
  );
  assert.ok(body.length > 0, "缺 saveSubscription");
  assert.match(body, /presence:\s*true/, "订阅必须声明 presence 能力，否则服务端永远 fail-open");
  assert.match(body, /client_id:\s*presenceClientID\(\)/, "订阅上报必须带本标签页的 client_id");
});

test("订阅成功后立刻开始心跳（含存量订阅启动路径）", async () => {
  const service = read("src/services/webPush.ts");
  const subscribe = service.slice(service.indexOf("export async function subscribeWebPush"));
  assert.match(
    subscribe.slice(0, subscribe.indexOf("export async function unsubscribeWebPush")),
    /startWebPushPresence\(subscription\.endpoint\)/,
    "手动订阅成功后必须开始心跳",
  );
  const ensure = service.slice(service.indexOf("export async function ensureWebPushSubscribed"));
  assert.match(
    ensure,
    /await saveSubscription\(subscription, currentPlatform\(\)\);[\s\S]{0,400}startWebPushPresence\(subscription\.endpoint\)/,
    "每次启动的幂等回传之后必须开始心跳：存量订阅不会被悄悄漏掉",
  );
  const unsubscribe = service.slice(service.indexOf("export async function unsubscribeWebPush"));
  assert.match(unsubscribe, /stopWebPushPresence\(\)/, "退订必须停掉心跳");
});

test("心跳模块：30s 一次，关页 beacon，回前台补心跳", async () => {
  const presence = read("src/services/webPushPresence.ts");
  assert.match(
    presence,
    /PRESENCE_HEARTBEAT_MS\s*=\s*30_000/,
    "心跳间隔必须显著小于服务端 TTL（10 分钟）",
  );
  assert.match(
    presence,
    /setInterval\(touch, PRESENCE_HEARTBEAT_MS\)/,
    "必须有周期心跳",
  );
  assert.match(
    presence,
    /addEventListener\("pagehide", onPageHide\)/,
    "关页必须立即上报（不能等 TTL 过期）",
  );
  assert.match(
    presence,
    /navigator\.sendBeacon\(url, blob\)/,
    "关页上报必须走 sendBeacon：fetch 在卸载阶段会被丢掉",
  );
  assert.match(
    presence,
    /new Blob\(\[body\], \{ type: "text\/plain;charset=UTF-8" \}\)/,
    "beacon 正文必须是 CORS 安全清单内的类型，否则跨源部署下预检失败、上报丢失",
  );
  assert.match(
    presence,
    /visibilityState === "visible"[\s\S]{0,120}touch\(\)/,
    "回到前台必须补一次心跳，否则后台超过 TTL 的标签页收不到通知",
  );
  assert.match(
    presence,
    /event\.persisted[\s\S]{0,80}touch\(\)/,
    "bfcache 恢复必须重新登记在场",
  );
  assert.match(
    presence,
    /sessionStorage/,
    "client_id 必须按标签页存：关一个标签页不能把同订阅下另一个还开着的标签页判死",
  );
});

test("service worker 在零窗口时自愈上报关页", async () => {
  const vite = read("vite.config.ts");
  const sw = vite.slice(vite.indexOf("async function reportClosedPageToServer"));
  assert.ok(sw.length > 0, "缺 reportClosedPageToServer");
  assert.match(
    sw.slice(0, sw.indexOf("self.addEventListener")),
    /clients\.matchAll\(\{ type: "window", includeUncontrolled: true \}\)/,
    "必须按窗口客户端判断页面是否真的都不在了",
  );
  assert.match(sw, /leave:\s*true/, "零窗口时必须上报 leave");
  assert.match(
    sw,
    /new URL\("api\/web-push\/presence", self\.registration\.scope\)/,
    "上报地址必须跟着 SW 作用域（部署前缀），不能写死 /api",
  );
  const push = vite.slice(vite.indexOf('self.addEventListener("push"'));
  const selfHealAt = push.indexOf("reportClosedPageToServer()");
  const showAt = push.indexOf("showNotification(");
  assert.ok(selfHealAt > 0 && showAt > selfHealAt, "自愈必须先于 showNotification 发出");
  assert.match(
    push,
    /showNotification/,
    "自愈不得顶掉通知：userVisibleOnly 订阅不显示通知会被 Chrome 换成兜底提示",
  );
});
