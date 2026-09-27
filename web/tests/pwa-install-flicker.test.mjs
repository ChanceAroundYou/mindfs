import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// PWA 安装按钮「闪一下再消失」的回归守卫。
//
// 背景：beforeinstallprompt 每个页面生命周期只派发一次，而承载它的状态原先放在
// FileTree 的组件局部 state 里。移动端关闭侧栏时 AppShell 整块卸载 <aside>
// （{(!isMobile || physicalLeftOpen) && physicalLeftContent ? <aside>…}），
// 侧栏一关一开就是全新实例、state 归零，而那个一次性事件不会重放。
// 于是每次打开侧栏都先按「还没结论」画一帧，再被清掉 —— 按钮闪一下又消失，
// 同时 hasFooterContent 带着 footer 的 padding/borderTop 一起塌回 0（底栏收起）。
//
// 这里守的是「事件由模块级单例持有、组件只订阅」这条结构不变量。
// 闪烁本身是渲染 + 事件时序问题，静态断言测不出，真机验证见计划里的清单。

const fileTree = readFileSync(new URL("../src/components/FileTree.tsx", import.meta.url), "utf8");
const service = readFileSync(new URL("../src/services/pwaInstall.ts", import.meta.url), "utf8");
const appShell = readFileSync(new URL("../src/layout/AppShell.tsx", import.meta.url), "utf8");

// 1) 一次性事件必须活过组件卸载 —— 监听在模块单例里挂一次，不在组件 effect 里反复挂。
assert.match(
  service,
  /window\.addEventListener\("beforeinstallprompt", this\.handleBeforeInstallPrompt\)/,
  "beforeinstallprompt 必须在模块级单例里挂载（否则组件卸载后事件丢失）",
);
assert.match(
  service,
  /export const pwaInstallService = new PwaInstallService\(\)/,
  "应导出一个进程级单例，供组件跨卸载重挂读取",
);

// 2) FileTree 必须订阅单例，不能再自己 addEventListener 这个一次性事件。
assert.match(
  fileTree,
  /pwaInstallService\.subscribe\(/,
  "FileTree 应订阅 pwaInstallService 快照",
);
assert.doesNotMatch(
  fileTree,
  /addEventListener\("beforeinstallprompt"/,
  "FileTree 不得自己监听 beforeinstallprompt（会把一次性事件绑死在组件生命周期上）",
);
assert.doesNotMatch(
  fileTree,
  /addEventListener\("appinstalled"/,
  "FileTree 不得自己监听 appinstalled（同上）",
);

// 3) 「还没结论」与「不能装」必须分开：Android Chrome 上判定未落定前不画按钮，
//    否则它会先凭空出现再消失。
assert.match(
  fileTree,
  /const installProbePending = isAndroidChrome && !installProbeDone;/,
  "应显式区分「判定中」与「不能装」",
);
assert.match(
  fileTree,
  /const shouldShowInstallButton = !installProbePending && !isNativeApp && !isKnownInstalled/,
  "按钮可见性必须以 installProbePending 为前置门",
);
assert.match(
  fileTree,
  /const shouldShowInstallHelp = !installProbePending && !isNativeApp/,
  "说明文字同样不得在判定完成前出现",
);

// 4) 单例的 probed 只在拿到结论时置位，不能一上来就置 true（否则前置门形同虚设）。
assert.match(service, /probed: false/, "可安装的平台初始应为 probed:false");
assert.match(
  service,
  /deferredPrompt: event as BeforeInstallPromptEvent, probed: true/,
  "事件到达才算落定 probed",
);

// 5) 消费一次性 event 时必须同时落定 probed，否则用户关掉安装框后按钮会消失。
assert.match(
  service,
  /this\.patch\(\{ deferredPrompt: null, probed: true \}\)/,
  "consumeDeferredPrompt 清空 event 时必须一并落定 probed",
);

// 6) 守护前提：移动端侧栏确实会卸载（否则第 1 条的前提不成立，注释也会变成误导）。
assert.match(
  appShell,
  /\{\(!isMobile \|\| physicalLeftOpen\) && physicalLeftContent \?/,
  "AppShell 在移动端关闭侧栏时仍会卸载 <aside> —— 单例方案正是为此而立",
);
// 7) localStorage 键不得改动（已装用户的历史标记要继续生效）。
assert.match(service, /const INSTALLED_KEY = "mindfs-pwa-installed";/, "已安装标记的 localStorage 键保持不变");
