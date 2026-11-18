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

test("定时唤醒兜底（Doze 下限 15 分钟）", async () => {
  const service = read(SERVICE);
  assert.match(service, /ACTION_WATCHDOG/, "缺 watchdog 动作");
  assert.match(service, /setAndAllowWhileIdle/, "闹钟必须能在 Doze 下唤醒");
  assert.match(service, /scheduleWatchdog\(\)/, "resumePolling 必须续约 watchdog");
  assert.match(service, /cancelWatchdog\(\)/, "stopAll 必须取消 watchdog");
});
