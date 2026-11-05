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
  assert.match(ts, /api\.github\.com\/repos\/ChanceAroundYou\/mindfs\/releases/);
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
