// 常驻回复通知可点直达 + 前台服务类型改 specialUse 的源码守卫（Task 7）。
//
// 钉住四件事：
//   1) 常驻通知带 contentIntent：恰好 1 个 live 会话时直达该会话，0 个或多个只拉起 App；
//   2) extras 每次都按当次 live 重新算，不带名字（隐私），计数变化后不会残留单会话信息；
//   3) manifest 用 specialUse 类型 + FOREGROUND_SERVICE_SPECIAL_USE 权限 +
//      PROPERTY_SPECIAL_USE_FGS_SUBTYPE 子类型说明，不再把 dataSync 当唯一类型；
//   4) startForegroundCompat 在 API 34+ 用 SPECIAL_USE，Q–33 保留 dataSync，低于 Q 不带类型。
import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

const read = (relative) =>
  fs.readFileSync(path.resolve(import.meta.dirname, "..", relative), "utf8");

const SERVICE = "../web/android/app/src/main/java/com/mindfs/app/ReplyPollerService.java";
const MANIFEST = "../web/android/app/src/main/AndroidManifest.xml";

// 按花括号配平取方法正文；被断言的方法正文里没有带花括号的字符串字面量。
function methodBody(source, signature) {
  const start = source.indexOf(signature);
  assert.notEqual(start, -1, `找不到方法签名：${signature}`);
  const open = source.indexOf("{", start);
  assert.notEqual(open, -1, `方法没有正文：${signature}`);
  let depth = 0;
  for (let i = open; i < source.length; i++) {
    if (source[i] === "{") depth++;
    else if (source[i] === "}") {
      depth--;
      if (depth === 0) return source.slice(open, i + 1);
    }
  }
  throw new Error(`方法未闭合：${signature}`);
}

test("常驻通知可点：恰好 1 个 live 会话直达，其余只拉起 App", async () => {
  const service = read(SERVICE);
  const idle = methodBody(
    service,
    "private Notification buildIdleNotification(String text, ReplyStateMerger.Entry target)",
  );
  const update = methodBody(
    service,
    "private void updateForegroundNotification(List<ReplyStateMerger.Entry> live)",
  );

  // 常驻通知必须可点，否则用户只能自己去应用里找。
  assert.match(idle, /setContentIntent\(/, "常驻通知缺 contentIntent");
  // 直达信息与 postAlert 同一套：nodeId/rootId/sessionKey extras。
  assert.match(idle, /putExtra\("nodeId"/, "常驻通知缺 nodeId extra");
  assert.match(idle, /putExtra\("rootId"/, "常驻通知缺 rootId extra");
  assert.match(idle, /putExtra\("sessionKey"/, "常驻通知缺 sessionKey extra");
  // 只有 caller 确认「恰好一个」时才把目标传进来。
  assert.match(
    idle,
    /if \(target != null\)[\s\S]{0,200}putExtra\("nodeId"/,
    "extras 必须在 target != null 分支内",
  );
  // 启动 Intent 抄 postAlert 的构造：launch intent + SINGLE_TOP|CLEAR_TOP。
  assert.match(idle, /getLaunchIntentForPackage\(getPackageName\(\)\)/, "没复用启动 Intent");
  assert.match(idle, /FLAG_ACTIVITY_SINGLE_TOP \| Intent\.FLAG_ACTIVITY_CLEAR_TOP/);
  // requestCode 固定 FOREGROUND_ID，且每次都用 UPDATE_CURRENT 重发（extras 跟着当次 live 走）。
  assert.match(
    idle,
    /PendingIntent\.getActivity\(\s*this,\s*FOREGROUND_ID,\s*launchIntent,\s*PendingIntent\.FLAG_UPDATE_CURRENT \| PendingIntent\.FLAG_IMMUTABLE\)/,
    "常驻通知的 PendingIntent 必须用 FOREGROUND_ID + UPDATE_CURRENT",
  );
  // 计数由合并器给的 live 决定：只有 size()==1 才传该条，否则传 null。
  assert.match(update, /live\.size\(\) == 1/, "updateForegroundNotification 必须按 live.size()==1 判定");
  assert.match(update, /buildIdleNotification\(text, target\)/, "单个会话要把目标传进通知");
  assert.match(update, /target = live\.size\(\) == 1 \? live\.get\(0\) : null/, "多会话/零会话必须传 null");
  // 空列表分支同样可点（普通启动 Intent）。
  assert.match(update, /buildIdleNotification\(\)/);
});

test("常驻通知文案不含会话名（隐私）", async () => {
  const service = read(SERVICE);
  const idle = methodBody(
    service,
    "private Notification buildIdleNotification(String text, ReplyStateMerger.Entry target)",
  );
  const update = methodBody(
    service,
    "private void updateForegroundNotification(List<ReplyStateMerger.Entry> live)",
  );

  assert.doesNotMatch(idle, /sessionTitle|rootTitle/, "常驻通知不能读会话/项目名");
  assert.doesNotMatch(update, /sessionTitle|rootTitle/, "常驻通知不能读会话/项目名");
  assert.match(update, /"有会话正在回复"/);
  assert.match(update, /live\.size\(\) \+ " 个会话回复中"/);
});

test("manifest 声明 specialUse 前台服务类型与子类型", async () => {
  const manifest = read(MANIFEST);
  // service 元素可能是自闭合的，也可能因为带 <property> 子元素而展开。
  const start = manifest.indexOf("<service");
  assert.notEqual(start, -1, "manifest 里找不到 service 元素");
  const closing = manifest.indexOf("</service>", start);
  const serviceTag =
    closing === -1
      ? manifest.slice(start, manifest.indexOf("/>", start) + 2)
      : manifest.slice(start, closing + "</service>".length);

  assert.match(manifest, /android:foregroundServiceType="specialUse/);
  assert.ok(
    !manifest.includes('android:foregroundServiceType="dataSync"'),
    "manifest 仍把 dataSync 当唯一前台服务类型",
  );
  assert.match(manifest, /android\.permission\.FOREGROUND_SERVICE_SPECIAL_USE/);
  assert.match(serviceTag, /android\.app\.PROPERTY_SPECIAL_USE_FGS_SUBTYPE/);
  assert.match(serviceTag, /android:value="session reply sync"/);
});

test("startForegroundCompat 按版本选类型", async () => {
  const service = read(SERVICE);
  const body = methodBody(service, "private void startForegroundCompat(int notificationId, Notification notification)");

  // 34+（Android 14）走 SPECIAL_USE：manifest 的 specialUse 类型才生效。
  assert.match(
    body,
    /Build\.VERSION\.SDK_INT >= Build\.VERSION_CODES\.UPSIDE_DOWN_CAKE[\s\S]{0,160}FOREGROUND_SERVICE_TYPE_SPECIAL_USE/,
    "API 34+ 必须用 SPECIAL_USE 类型",
  );
  // Q–33 继续用 dataSync（旧系统不认识 specialUse 位）。
  assert.match(
    body,
    /Build\.VERSION\.SDK_INT >= Build\.VERSION_CODES\.Q[\s\S]{0,160}FOREGROUND_SERVICE_TYPE_DATA_SYNC/,
    "Q–33 仍走 dataSync 类型",
  );
  // Q 以下没有类型参数。
  assert.match(body, /startForeground\(notificationId, notification\);/);
});