// 网页版后台提醒的契约。
//
// 用户报的现象：「网页在后台不会及时提示，只会在切到前台时响提示音」。
// 根因有两条，都在前端生命周期上，所以这里用源码级守卫钉住：
//   1) 5s 轮询原来在 document.visibilityState !== "visible" 时直接 return，
//      等于连「后台标签页 1 次/分钟」都放弃了；
//   2) play() 只在 AudioContext.state === "running" 时出声，后台被挂起的上下文
//      会让那条提醒静默丢失，切回前台才补。
// 真正的后台通道是 Web Push —— 它必须由 App 自动建立，不能依赖用户去通知面板点开关。
import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

const read = (relative) =>
  fs.readFileSync(path.resolve(import.meta.dirname, "..", relative), "utf8");

const stripComments = (source) =>
  source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");

test("跨节点轮询在后台仍然执行（只是被浏览器节流）", async () => {
  const poll = stripComments(read("src/app/useReplyingSessionPoll.ts"));
  const intervalCall = poll.slice(poll.indexOf("setInterval"));
  assert.ok(intervalCall.length > 0, "轮询定时器不见了");
  const body = intervalCall.slice(0, intervalCall.indexOf("}, REMOTE_REPLY_POLL_MS)"));
  assert.ok(
    !body.includes('visibilityState !== "visible"'),
    "定时器里不能再按可见性短路：后台标签页的节流轮询是完成时刻的唯一页面内来源",
  );
  assert.ok(
    body.includes("refreshMultiProjectReplyingSessions()"),
    "定时器必须真的去拉一次",
  );
});

test("提示音在被挂起的 AudioContext 上先 resume 再排音", async () => {
  const sound = stripComments(read("src/app/useNotificationSound.ts"));
  assert.ok(
    !/if \(!ctx \|\| ctx\.state !== "running"\) return;/.test(sound),
    "play() 不能在上下文未运行时静默丢弃这条提醒",
  );
  assert.match(
    sound,
    /ctx\.resume\(\)[\s\S]{0,200}schedule\(\)/,
    "挂起时应当先 resume，成功后立刻排音",
  );
  assert.match(
    sound,
    /document\.addEventListener\("visibilitychange", resumeOnVisible\)/,
    "回到前台必须恢复音频上下文，否则后台期间积压的提醒永远不出声",
  );
});

test("App 启动时自动建立 Web Push 订阅", async () => {
  const app = read("src/App.tsx");
  assert.match(app, /import \{ useBackgroundPush \}/, "缺 useBackgroundPush 接线");
  assert.match(app, /useBackgroundPush\(\)/, "App 必须挂上后台推送订阅");

  const push = read("src/app/useBackgroundPush.ts");
  assert.match(push, /ensureWebPushSubscribed\(\{ prompt \}\)/, "必须调用自动订阅");
  assert.match(push, /isNativeShellRuntime\(\)/, "原生壳不参与 Web Push（它有自己的常驻服务）");
  assert.match(push, /needsNotificationPermission\(\)/, "权限还是 default 时才等用户手势弹框");
  assert.match(push, /backgroundPushDeclined\(\)/, "用户拒绝过就不再弹框");
});

test("ensureWebPushSubscribed 只在已授权时静默订阅", async () => {
  const service = read("src/services/webPush.ts");
  const body = service.slice(service.indexOf("export async function ensureWebPushSubscribed"));
  assert.ok(body.length > 0, "缺 ensureWebPushSubscribed");
  const head = body.slice(0, body.indexOf("const registration = await"));
  assert.match(
    head,
    /if \(!options\.prompt\) \{\s*return getWebPushStatus\(\);/,
    "权限为 default 且未要求弹框时必须直接返回，不能偷偷弹框",
  );
  assert.match(head, /Notification\.permission === "denied"/, "已拒绝的权限不再尝试");
  assert.match(body, /saveSubscription\(subscription, currentPlatform\(\)\)/, "订阅必须回传服务端（幂等 upsert）");
});
