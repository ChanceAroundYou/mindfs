import assert from "node:assert/strict";
import { readFileSync } from "./source-map-fs.mjs";
import { readGoPackage } from "./go-source.mjs";
import { test } from "vitest";

test("desktop-shell-install-cta", async () => {
// 桌面套壳里不该出现 PWA「安装应用」入口。
//
// 背景：Windows 边缘端的 GUI 是安装器装好的 WebView2 套壳，页面却仍然在左侧栏底部
// 常驻一条「安装应用 / 可从地址栏安装图标或浏览器菜单中安装」。这两句对套壳都是错的：
// 程序已经装好了，WebView2 也没有可安装的浏览器上下文（点下去只会弹一句去地址栏安装）。
//
// 判据不能并进 isNativeShellRuntime()：Windows 套壳没有 MindFSNative 桥，一旦被当成
// 原生壳，原生下载/拉起/推送那些分支会走一条不存在的桥。所以用独立标记
// __MIND_FS_DESKTOP_SHELL__，由套壳在文档创建前注入，前端只拿它关掉安装入口。

const runtime = readFileSync(new URL("../src/services/runtime.ts", import.meta.url), "utf8");
const hook = readFileSync(new URL("../src/hooks/usePwaInstall.ts", import.meta.url), "utf8");
const goShell = readGoPackage("cli/cmd");

// 1) 前端读的全局名必须与套壳注入的全局名一致（跨端字符串，改名时两边一起改）。
assert.match(runtime, /__MIND_FS_DESKTOP_SHELL__/, "runtime 必须读取 __MIND_FS_DESKTOP_SHELL__");
assert.match(goShell, /__MIND_FS_DESKTOP_SHELL__ = true/, "套壳注入脚本必须设置同一个全局");
assert.match(
  goShell,
  /const desktopShellInitScript = "window\.__MIND_FS_DESKTOP_SHELL__ = true;"/,
  "注入脚本应是文档创建前的常量，而不是运行期 Eval",
);

// 2) 注入必须发生在任何导航之前：AddScriptToExecuteOnDocumentCreated 只对新文档生效。
const initIdx = goShell.indexOf("wv.Init(desktopShellInitScript)");
const navIdx = goShell.indexOf("s.wv.Navigate(");
assert.ok(initIdx >= 0, "套壳必须在创建 WebView2 后调用 wv.Init(desktopShellInitScript)");
assert.ok(navIdx >= 0, "套壳仍应通过 Navigate 打开页面");
assert.ok(initIdx < navIdx, "注入必须先于 Navigate，否则首屏拿不到标记");

// 3) 前端有独立的判定函数，且不顺带改变原生壳定义。
assert.match(runtime, /export function isDesktopShellRuntime\(\): boolean \{/, "应导出 isDesktopShellRuntime()");
assert.match(
  runtime,
  /__MIND_FS_DESKTOP_SHELL__ === true/,
  "判定必须严格等于 true（缺省的 undefined 不能算套壳）",
);
assert.match(
  runtime,
  /export function isDesktopShellRuntime\(\): boolean \{[\s\S]{0,600}__MIND_FS_DESKTOP_SHELL__ === true[\s\S]{0,200}Boolean\(w\.chrome\?\.webview\)[\s\S]{0,40}\}/,
  "套壳判定认注入标记，也认 WebView2 自带的 chrome.webview（旧套壳二进制不必同升）",
);
// 误伤防线：普通 Edge/Chrome 标签页没有 chrome.webview，判定必须要求它存在，
// 而不是只看「有 window.chrome」。
assert.doesNotMatch(
  runtime,
  /Boolean\(w\.chrome\)/,
  "不能把任何带 window.chrome 的浏览器都当成套壳",
);

// 4) 安装入口的三处可见性判定都要被套壳关掉，并且连 beforeinstallprompt 监听都不挂。
assert.match(
  runtime,
  /return !isNativeShellRuntime\(\) && !isDesktopShellRuntime\(\);/,
  "shouldEnablePWAInstall 在套壳里应直接返回 false",
);
assert.match(
  hook,
  /const shouldShowInstallButton = !installProbePending && !isNativeApp && !isDesktopShell/,
  "安装按钮在套壳里必须隐藏",
);
assert.match(
  hook,
  /const shouldShowInstallHelp = !installProbePending && !isNativeApp && !isDesktopShell/,
  "按钮下方的安装说明同样必须隐藏",
);
assert.match(
  hook,
  /const installProbePending = [\s\S]{0,200}&& !isDesktopShell;/,
  "套壳里不存在「判定中」——不应为安装事件等待一帧",
);
});
