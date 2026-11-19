// 安卓壳后台提醒的存活契约。
//
// 用户报的现象：「移动端 app 在后台，时间一长也完全不会发生新提示」。
// nginx 侧证据：每次断流（原生 /wait 请求被客户端 abort → 499）之后都跟着一段静默，
// 最长的 104 分钟内手机仍在正常访问同机其他服务 —— 设备与网络都活着，只有 MindFS 的
// 后台轮询死了。根因是四条生命周期缺口，逐条在这里钉住：
//   1) onStartCommand 对系统粘性重启给的 null intent 直接 START_NOT_STICKY 返回：
//      服务起来了、前台通知在、循环一条没开；
//   2) stopWithTask=true：从最近任务里划掉 app 会连带杀掉常驻服务；
//   3) 没有开机 / 安装更新后的自启（START_STICKY 不跨重启、不跨重装）；
//   4) 长轮询失败一律停 10 分钟（固定 WAIT_RETRY_INTERVAL_MS），连一次隧道抖动都要等满。
import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

const read = (relative) =>
  fs.readFileSync(path.resolve(import.meta.dirname, "..", relative), "utf8");

const SERVICE = "android/app/src/main/java/com/mindfs/app/ReplyPollerService.java";
const MANIFEST = "android/app/src/main/AndroidManifest.xml";
const RECEIVER = "android/app/src/main/java/com/mindfs/app/ReplyPollerBootReceiver.java";
const POLICY = "android/app/src/main/java/com/mindfs/app/ReplyRetryPolicy.java";

test("服务被系统粘性重启时必须恢复轮询，而不是空转", async () => {
  const service = read(SERVICE);
  const onStart = service.slice(
    service.indexOf("public int onStartCommand"),
    service.indexOf("public IBinder onBind"),
  );
  assert.match(
    onStart,
    /if \(intent == null\) \{[\s\S]{0,200}resumePolling\(\);[\s\S]{0,120}return START_STICKY;/,
    "null intent（进程被杀后重建）必须调用 resumePolling 并返回 START_STICKY",
  );
});

test("从最近任务划掉 app 不杀常驻轮询", async () => {
  const manifest = read(MANIFEST);
  const service = manifest.slice(manifest.indexOf('android:name=".ReplyPollerService"'));
  const tag = service.slice(0, service.indexOf("</service>"));
  assert.ok(
    tag.includes('android:stopWithTask="false"'),
    "stopWithTask 必须为 false：划卡片不该静音后台提醒",
  );
});

test("开机与安装更新后自动拉起轮询", async () => {
  const manifest = read(MANIFEST);
  assert.match(manifest, /android:name="\.ReplyPollerBootReceiver"/, "缺开机接收器声明");
  assert.match(manifest, /android\.intent\.action\.BOOT_COMPLETED/, "缺 BOOT_COMPLETED");
  assert.match(manifest, /android\.intent\.action\.MY_PACKAGE_REPLACED/, "缺 MY_PACKAGE_REPLACED（装完 APK 进程不保留）");
  assert.match(
    manifest,
    /android\.permission\.RECEIVE_BOOT_COMPLETED/,
    "缺 RECEIVE_BOOT_COMPLETED：没有它广播收不到",
  );

  const receiver = read(RECEIVER);
  assert.match(receiver, /isConfigured\(context\)/, "没配置过就不该拉起前台服务");
  assert.match(receiver, /startForegroundService/, "必须用 startForegroundService（Android 8+）");
  assert.match(receiver, /ReplyPollerService\.ACTION_WATCHDOG/, "统一走幂等的 watchdog 入口");
});

test("退避不再出现 10 分钟空窗，且回到前台立刻重试", async () => {
  const service = read(SERVICE);
  assert.ok(
    !service.includes("WAIT_RETRY_INTERVAL_MS"),
    "固定 10 分钟的重试窗口必须删掉（网络抖动不该静音十分钟）",
  );
  assert.match(service, /ReplyRetryPolicy\.retryDelayMillis\(/, "长轮询失败要走指数退避");
  assert.match(service, /resumeSignal/, "回到前台要有信号让退避中的循环提前醒来");

  const policy = read(POLICY);
  assert.match(policy, /RETRY_BASE_MILLIS = 15_000L/);
  assert.match(policy, /RETRY_MAX_MILLIS = 60_000L/);
});

test("电池优化白名单状态可见、可申请", async () => {
  const manifest = read(MANIFEST);
  assert.match(
    manifest,
    /android\.permission\.REQUEST_IGNORE_BATTERY_OPTIMIZATIONS/,
    "缺申请表权限：Doze / 厂商清理会让后台轮询长期失效",
  );
  const plugin = read("android/app/src/main/java/com/mindfs/app/ReplyPollerPlugin.java");
  assert.match(plugin, /public void batteryStatus\(/);
  assert.match(plugin, /public void requestBatteryExemption\(/);
  const service = read(SERVICE);
  assert.match(service, /电池优化=/, "诊断串必须报告电池优化状态");
});

test("定时唤醒兜底（3 分钟一次精确闹钟，不占状态栏）", async () => {
  const service = read(SERVICE);
  const receiver = read("android/app/src/main/java/com/mindfs/app/ReplyPollerWatchdogReceiver.java");
  assert.match(service, /ACTION_WATCHDOG/, "缺 watchdog 动作");
  // 2026-11-19：nginx 实测 8 小时里 110/63/158/54/22 分钟六段静默，15 分钟一次的
  // setAndAllowWhileIdle **一次都没响**（inexact 闹钟在国产 ROM 的省电模式里被无限推迟）。
  // 改用精确闹钟。具体 API 选 setExactAndAllowWhileIdle 而不是 setAlarmClock：后者会被
  // 系统当成「用户设的闹钟」，在状态栏常驻闹钟图标，用户明确不要。
  assert.match(service, /WATCHDOG_INTERVAL_MS = 3 \* 60_000L/, "巡检间隔必须是 3 分钟，15 分钟等于没有兜底");
  assert.match(service, /setExactAndAllowWhileIdle\(/, "必须用精确闹钟，inexact 闹钟会被省电模式吞掉");
  assert.doesNotMatch(service, /setAlarmClock\(/, "setAlarmClock 会在状态栏常驻闹钟图标，改用 setExactAndAllowWhileIdle");
  // 精确闹钟在 Android 12+ 对 targetSdk>=31 需要 SCHEDULE_EXACT_ALARM（或省电豁免）。
  // 未授权时它会抛 SecurityException，直接退回 inexact 就是「换了个名字的老毛病」——
  // 所以必须先判定授权，再把 inexact 当作兜底。
  assert.match(service, /canScheduleExactAlarms\(\)/, "排精确闹钟前必须判定授权，否则 SecurityException 会静默断掉巡检链");
  const idxGuard = service.indexOf("canScheduleExactAlarms()");
  const idxFallback = service.indexOf("alarms.setAndAllowWhileIdle");
  assert.ok(idxFallback > idxGuard, "inexact 只能是「没授权」时的兜底，不能是主路径");
  assert.match(
    read(MANIFEST),
    /android\.permission\.SCHEDULE_EXACT_ALARM/,
    "缺 SCHEDULE_EXACT_ALARM：Android 12+ 排不了精确闹钟",
  );
  assert.match(receiver, /ReplyPollerService\.WATCHDOG_INTERVAL_MS/, "接收器不能自己写一个间隔字面量");
  assert.doesNotMatch(receiver, /15 \* 60_000L/, "15 分钟的旧间隔必须消失");
  // 授予精确闹钟时系统会删掉先前排不上的闹钟并发这条广播；不重排的话授权后巡检链仍是断的。
  assert.match(
    read(MANIFEST),
    /android\.app\.action\.SCHEDULE_EXACT_ALARM_PERMISSION_STATE_CHANGED/,
    "缺授权变化广播：用户授权后闹钟不会自动重排",
  );
  assert.match(receiver, /ACTION_EXACT_ALARM_PERMISSION_CHANGED/);
  assert.match(service, /scheduleWatchdog\(\)/, "resumePolling 必须续约 watchdog");
  assert.match(service, /cancelWatchdog\(\)/, "stopAll 必须取消 watchdog");
  // 间隔缩到 3 分钟后，pause 不取消巡检 = 「暂停」最多维持 3 分钟。
  assert.match(
    service,
    /private void pausePolling\(\) \{[\s\S]{0,300}?cancelWatchdog\(\);/,
    "暂停必须取消巡检，否则下一次闹钟会把轮询重新拉起来",
  );
  // 闹钟有没有真的响，是区分「ROM 冻进程」与「ROM 直接清掉并取消闹钟」的唯一证据：
  // 前者时间戳持续前进，后者停在某一点。诊断串必须把它暴露出来。
  assert.match(service, /PREF_LAST_WAKE/, "必须记录最近一次 watchdog 唤醒");
  assert.match(service, /最近唤醒=/, "诊断串必须展示最近唤醒时间");
});
