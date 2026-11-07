// iOS 壳（iPad 套壳）的机器可核对契约。
//
// 钉住五件事（均为接线、非文案；改名/换实现允许，只不许断链）：
//   1) runtime 认 ios 平台（Capacitor 回报 \"ios\" → \"ios\"），SW/PWA 在壳里关闭；
//   2) iOS 没有自定义 Swift 插件：supportsCustomNativePlugins() = !isIOSRuntime()，且四个
//      调用点（replyPoller 的 configure/dismiss、nativeCacheControl、launcherNodeSync、
//      nativeDownload）各自短路 —— 安卓 Java 插件的调用在 iOS 上只会得「插件未注册」；
//   3) iOS 的下载/外链/版本信息走官方插件（@capacitor/filesystem、@capacitor/browser、
//      @capacitor/app），关于弹窗的假版本号与「检查更新/测试提醒」按钮不许在 iOS 上出现；
//   4) 构建接线：npm build:shell 产 dist-shell（安卓/iOS 共用），Makefile 有 build-ios，且
//      iOS 不在 make release 里（不会把 .ipa 当 release 资产推给安卓自更新）；
//   5) web/ios/App 工程在仓（与 web/android 对称）：xcodeproj、Info.plist、Package.swift、
//      AppIcon 非空白、cap sync 的 packageClassList 含六个官方插件。
import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

const root = path.resolve(import.meta.dirname, "..", "..");
const read = (file) => fs.readFileSync(path.join(root, file), "utf8");
const exists = (file) => fs.existsSync(path.join(root, file));

test("runtime recognizes ios and disables SW/PWA inside the shell", () => {
  const runtime = read("web/src/services/platform/runtime.ts");
  assert.match(runtime, /capacitorPlatform === "ios" \? "ios"/);
  assert.match(runtime, /export function isIOSRuntime\(\)/);
  assert.match(runtime, /export function shouldRegisterServiceWorker\(\)[^}]*return !isNativeShellRuntime\(\)/);
  assert.match(runtime, /export function shouldEnablePWAInstall\(\)[^}]*return !isNativeShellRuntime\(\)/);
});

test("custom android plugins are short-circuited on ios", () => {
  const runtime = read("web/src/services/platform/runtime.ts");
  assert.match(runtime, /export function supportsCustomNativePlugins\(\)[^}]*return !isIOSRuntime\(\)/);

  const poller = read("web/src/services/net/replyPoller.ts");
  assert.match(poller, /if \(!hasNativeBridge && !supportsCustomNativePlugins\(\)\) \{/);
  assert.match(poller, /if \(!supportsCustomNativePlugins\(\)\) \{/);

  const cacheControl = read("web/src/services/platform/nativeCacheControl.ts");
  assert.match(cacheControl, /!supportsCustomNativePlugins\(\)/);

  const launcherSync = read("web/src/services/platform/launcherNodeSync.ts");
  assert.match(launcherSync, /if \(!supportsCustomNativePlugins\(\)\) \{/);
});

test("ios uses official plugins for download, browser and app info", () => {
  const download = read("web/src/services/platform/download.ts");
  assert.match(download, /@capacitor\/filesystem/);
  assert.match(download, /downloadWithIOSShell/);
  assert.match(download, /Directory\.Documents/);
  assert.match(download, /if \(!supportsCustomNativePlugins\(\)\) \{\s*return downloadWithIOSShell/);

  const nav = read("web/src/services/platform/platformNavigation.ts");
  assert.match(nav, /openWithCapacitorBrowser/);
  assert.match(nav, /@capacitor\/browser/);

  const appUpdate = read("web/src/services/update/appUpdate.ts");
  // 自更新仍只归安卓：iOS 壳内部不显示「有新版本可装」。
  assert.match(appUpdate, /export function isUpdatableNativeRuntime\(\)[^}]*return isAndroidRuntime\(\)/);
  assert.match(appUpdate, /export async function getAppInfo\(\)/);
  assert.match(appUpdate, /@capacitor\/app/);

  // 关于弹窗：版本信息走 useNativeShellAppVersion（getAppInfo），不再写死假版本号；
  // 「检查更新」「测试提醒」在不支持时按钮不渲染。
  const dialog = read("web/src/components/file/FileTreeAboutDialog.tsx");
  assert.match(dialog, /useNativeShellAppVersion/);
  assert.doesNotMatch(dialog, /web \(0\.5\.5/, "关于弹窗又写死了一个假版本号");
  assert.match(dialog, /\{appVersion \|\| "web"\}/);
  assert.match(dialog, /isNativeApp && apkUpdate\.supported/);
  assert.match(dialog, /isNativeApp && replyAlert\.supported/);

  const apkHook = read("web/src/hooks/useApkUpdateCheck.ts");
  assert.match(apkHook, /supported: isAndroidRuntime\(\)/);
  const alertHook = read("web/src/hooks/useTestNativeReplyAlert.ts");
  assert.match(alertHook, /supported: supportsCustomNativePlugins\(\)/);
});

test("shell bundle is shared and iOS never enters make release", () => {
  const pkg = read("web/package.json");
  assert.match(pkg, /"build:shell": "rimraf dist-shell && VITE_APP_SHELL=1 vite build --outDir dist-shell --emptyOutDir"/);
  assert.doesNotMatch(pkg, /"build:android":/, "dist-android 旧入口不应回来");

  const cap = read("web/capacitor.config.json");
  assert.match(cap, /"webDir":\s*"dist-shell"/);
  assert.match(cap, /https:\/\/home\.xiaokubao\.space\/mindfs\//);

  const makefile = read("Makefile");
  assert.match(makefile, /^build-ios:/m);
  assert.match(makefile, /cap sync ios/);
  // build-ios 不得作为 make release 的依赖：.ipa 不是 release 资产。
  const lines = makefile.split("\n");
  const releaseIdx = lines.findIndex((line) => line.startsWith("release:"));
  assert.ok(releaseIdx >= 0, "找不到 release target");
  const restLines = lines.slice(releaseIdx);
  const nextIdx = restLines.slice(1).findIndex((line) => /^\S+:/.test(line));
  const releaseBlock = nextIdx >= 0 ? restLines.slice(0, nextIdx + 1).join("\n") : restLines.join("\n");
  assert.ok(releaseBlock, "找不到 release target");
  assert.doesNotMatch(releaseBlock, /build-ios/, ".ipa 不是 release 资产，build-ios 不得进 make release");
  assert.match(makefile, /^build-ios:[\s\S]*?cap sync ios/m);

  const gitignore = read(".gitignore");
  assert.match(gitignore, /^dist-shell\/$/m);
});

test("iOS Xcode project is checked in and synced plugin list", () => {
  const keyFiles = [
    "web/ios/App/App.xcodeproj/project.pbxproj",
    "web/ios/App/App/AppDelegate.swift",
    "web/ios/App/App/Info.plist",
    "web/ios/App/CapApp-SPM/Package.swift",
    "web/ios/App/App/capacitor.config.json",
  ];
  for (const file of keyFiles) {
    assert.ok(exists(file), `${file} 缺失（Xcode 工程未入仓）`);
  }

  const plist = read("web/ios/App/App/Info.plist");
  // iOS 版本身份：语义版本进 MARKETING_VERSION、构建号进 CURRENT_PROJECT_VERSION，
  // 两条都走 Xcode 变量（build-ipa.sh 从 git describe 直接塞值）。
  assert.match(plist, /\$\(MARKETING_VERSION\)/);
  assert.match(plist, /\$\(CURRENT_PROJECT_VERSION\)/);
  assert.match(plist, /UIViewControllerBasedStatusBarAppearance/);

  const capConfig = read("web/ios/App/App/capacitor.config.json");
  for (const plugin of ["AppPlugin", "CAPBrowserPlugin", "ClipboardPlugin", "FilesystemPlugin", "StatusBarPlugin"]) {
    assert.match(capConfig, new RegExp(`"${plugin}"`), `cap sync 后缺官方插件 ${plugin}`);
  }

  // app 图标不得是 Capacitor 出厂的纯白图：1024 设计图必须是非空白图。
  const iconPath = path.join(root, "web/ios/App/App/Assets.xcassets/AppIcon.appiconset/AppIcon-512@2x.png");
  assert.ok(fs.existsSync(iconPath), "AppIcon-512@2x.png 缺失");
  const icon = fs.readFileSync(iconPath);
  // PNG 是二进制：只做最小的「非空白」检查 —— 至少有足够字节，不是 1x1 空图。
  assert.ok(icon.length > 10000, `AppIcon 疑似出厂空白图（${icon.length} 字节）`);
});
