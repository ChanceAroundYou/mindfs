// APK 自更新的机器可核对契约。
//
// 钉住四件事：
//   1) 版本号比较公式在 build.gradle 与前端 TS 两份里一致（改一边等于永远「已是最新」或永远「有更新」）；
//   2) 安装链路在：manifest 有 REQUEST_INSTALL_PACKAGES、FileProvider 覆盖 external-files 目录、
//      下载目录有 updates 子目录、APK 用 application/vnd.android.package-archive 拉起；
//   3) JS 侧经 MindFSNativeDownload.downloadAndInstallApk 桥接，而不是打开浏览器跳 Release 页；
//   4) 关于弹窗的「检查更新」走 useApkUpdateCheck 并展示结果文案（不再静默消失）。
import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

const read = (relative) =>
  fs.readFileSync(path.resolve(import.meta.dirname, "..", relative), "utf8");

test("versionCode formula matches the gradle build", async () => {
  const gradle = read("../web/android/app/build.gradle");
  const ts = read("src/services/update/githubAppUpdate.ts");

  assert.match(gradle, /major \* 10000000 \+ minor \* 100000 \+ patch \* 1000 \+ build \+ 1/);
  assert.match(ts, /major \* 10000000 \+ minor \* 100000 \+ patch \* 1000 \+ build \+ 1/);
  // 主路径读发布清单（releases/latest/download 固定 URL），不再依赖未认证 REST API；
  // 未认证限流按出口 IP 计 60/h，机场出口常为 0 → 403「检查更新失败」。
  assert.match(ts, /releases\/latest\/download\/android-version\.json/);
  assert.match(ts, /releaseFromManifest/);
  // 发布 tag 是 v0.5.5-1108（不带 -g<sha>）：正则必须认它，否则 versionCode 落回 1，
  // 自更新永远判「已是最新」。
  assert.match(gradle, /\(\?:-\(\\d\+\)\(\?:-\\d\+\)\?\(\?:-g\[0-9a-f\]\+\.\*\)\?\)\?/);
});

test("android install chain is wired", async () => {
  const manifest = read("../web/android/app/src/main/AndroidManifest.xml");
  const filePaths = read("../web/android/app/src/main/res/xml/file_paths.xml");
  const plugin = read("../web/android/app/src/main/java/com/mindfs/app/NativeDownloadPlugin.java");
  const activity = read("../web/android/app/src/main/java/com/mindfs/app/MainActivity.java");

  assert.match(manifest, /android\.permission\.REQUEST_INSTALL_PACKAGES/);
  assert.match(filePaths, /<external-files-path/);
  assert.match(plugin, /"updates"\)/);
  assert.match(plugin, /application\/vnd\.android\.package-archive/);
  assert.match(plugin, /canRequestPackageInstalls/);
  assert.match(activity, /{\s*return NativeDownloadPlugin\.downloadAndInstallApk\(/);
  assert.match(activity, /"MindFSNativeDownload"/);
});

test("reply test alert creates its channel and reports blockers", async () => {
  const service = read("../web/android/app/src/main/java/com/mindfs/app/ReplyPollerService.java");
  const plugin = read("../web/android/app/src/main/java/com/mindfs/app/ReplyPollerPlugin.java");
  const hook = read("src/hooks/useTestNativeReplyAlert.ts");

  // 渠道只在 Service.onCreate 建 → 服务没跑过时测试提醒投进不存在的渠道，被系统静默丢弃。
  assert.match(service, /static void ensureChannels\(Context context\)/);
  assert.match(service, /public static String postTestAlert\(Context context\)/);
  assert.match(service, /ensureChannels\(context\);\s*\n\s*NotificationManager nm/, "postTestAlert ensures channels first");
  assert.match(service, /IMPORTANCE_NONE/, "closed channel is reported, not silently dropped");
  assert.match(plugin, /result\.put\("error", ReplyPollerService\.postTestAlert/);
  assert.match(hook, /测试提醒已发送/);
  assert.match(service, /postDelayed/, "second alert fires after the user leaves the app");
  assert.match(service, /PREF_DEBUG/, "poll results are recorded for diagnostics");
  assert.match(service, /setContentIntent/, "test alert gets a content intent so autoCancel can fire");
  // 通知栏里「已发送但看不见」只能靠系统实况区分：总开关/渠道/是否在栏里/勿扰。
  assert.match(service, /describeNotificationState/);
  assert.match(plugin, /result\.put\("diag"/);
  // 常驻（回复中）通知不点名，完成提醒才保留名称。
  assert.match(service, /有会话正在回复/, "ongoing notification hides session/root names");
  assert.match(service, /mindfs_reply_alert_v5/, "channel bumped so a muted v4 channel doesn't stick");
});

test("poller base URL keeps the mount prefix", async () => {
  const service = read("../web/android/app/src/main/java/com/mindfs/app/ReplyPollerService.java");
  const activity = read("../web/android/app/src/main/java/com/mindfs/app/MainActivity.java");
  const domain = read("src/app/useAppDomainLayer.ts");

  // 原生只能从 WebView URL 推出 origin，/mindfs 这类挂载前缀只有前端知道。
  // 切后台时若用 origin 覆盖前端上报的地址，轮询就打到 /api/… → 404，完成提醒永不出现。
  assert.match(service, /static boolean hasConfiguredBase\(Context context\)/);
  assert.match(activity, /if \(!ReplyPollerService\.hasConfiguredBase\(this\)\)/);
  assert.match(domain, /void syncNativeReplyPoller\(\)/);
  // appURL("/") 会带 ?user=<id>；原生是字符串直接拼，查询串必须在这里剥掉。
  assert.match(
    read("src/services/net/replyPoller.ts"),
    /resolved\.pathname\.replace\(\/\\\/\+\$\/, ""\)/,
    "base URL keeps only origin + path prefix",
  );
});

test("about dialog checks GitHub instead of opening the release page", async () => {
  const dialog = read("src/components/file/FileTreeAboutDialog.tsx");
  const hook = read("src/hooks/useApkUpdateCheck.ts");

  assert.match(dialog, /useApkUpdateCheck\(\)/);
  assert.match(dialog, /apkUpdate\.run\(\)/);
  assert.match(dialog, /apkUpdate\.message/);
  assert.doesNotMatch(dialog, /releases\/latest/, "no more redirect to the release page");
  assert.match(hook, /已是最新版本/);
  assert.match(hook, /installApkUpdate\(/);
});

test("auto update checks on launch and on returning to foreground", async () => {
  const hook = read("src/hooks/useApkAutoUpdate.ts");
  const prompt = read("src/components/app/ApkUpdatePrompt.tsx");
  const root = read("src/app/AppRootView.tsx");

  assert.match(hook, /isAndroidRuntime\(\)/, "only the native Android shell auto-checks");
  assert.match(hook, /addEventListener\("visibilitychange"/, "re-check when coming back to foreground");
  assert.match(hook, /AUTO_CHECK_INTERVAL_MS/, "throttled so resume doesn't hammer the GitHub API");
  assert.match(hook, /kind === "available"/, "only prompts when there really is a newer APK");
  assert.match(prompt, /发现新版本/);
  assert.match(prompt, /installApkUpdate|apply\(\)/, "installs from the prompt");
  assert.match(root, /<ApkUpdatePrompt \/>/, "prompt is mounted at the app root");
});

test("reply alerts are per-session and cross-node", async () => {
  const service = read("../web/android/app/src/main/java/com/mindfs/app/ReplyPollerService.java");
  const activity = read("../web/android/app/src/main/java/com/mindfs/app/MainActivity.java");
  const poller = read("src/services/net/replyPoller.ts");

  // 节点表由前端推送整条 URL（含挂载前缀与 user=），原生只 GET。
  assert.match(poller, /absoluteNodeURL\("\/api\/replying-sessions", id\)/);
  assert.match(poller, /nodes,/);
  assert.match(service, /private List<NodeTarget> nodeTargets\(\)/);
  // 复合键：三台机器上同名 root/session 不能互相覆盖。
  assert.match(service, /static String compositeKey\(String nodeId, String rootId, String sessionKey\)/);
  assert.match(service, /notificationIdFor\(String nodeId, String rootId, String sessionKey\)/);
  // 只对响应成功的节点判定「消失 = 完成」，离线节点不能当成任务结束。
  assert.match(service, /okNodes\.contains\(state\.nodeId\)/);
  // 正在看的会话不打扰；切到后台即清空。
  assert.match(service, /viewingKey\.equals\(state\.key\)/);
  assert.match(activity, /clearReplyViewing\(\)/);
  // 前台继续轮询（onResume 不再 pause）。
  assert.match(activity, /ensureReplyPollerRunning\(\)/);
  assert.doesNotMatch(activity, /onResume[\s\S]{0,200}pauseReplyPoller\(\)/, "resume must not pause polling");
  // 通知点击带 nodeId，跨节点可直达。
  assert.match(activity, /o\.put\("nodeId", safeNodeId\)/);
  assert.match(poller, /setNativeReplyViewing/);
});
