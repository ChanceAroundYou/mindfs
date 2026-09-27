import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// PWA 安装按钮「闪一下再消失」的回归守卫。
//
// 背景：按钮的可见性依赖 beforeinstallprompt，而它每页面生命周期只派发一次、
// 且要等 SW 激活才来（实测 0.2s~5s+）。移动端关闭侧栏时 AppShell 整块卸载 <aside>
// （{(!isMobile || physicalLeftOpen) && physicalLeftContent ? <aside>…}），
// 侧栏一关一开就是全新实例、组件 state 归零，而那个一次性事件不会重放。
//
// 真机录像（390x854@24fps，101 帧全量解码）显示：蓝色按钮整块（265x38，#3B82F6）
// 在 0 / 43 / 74 帧各存在**恰好一帧**（~42ms）就消失，其余帧与稳态逐像素一致 ——
// 是渲染翻转，不是布局抖动。
//
// 两处根因，各由下面一组断言守住：
//   (1) 事件绑在组件生命周期上 → 必须由模块级单例持有；
//   (2) 「判定中」的前置门只挂在 isAndroidChrome 上，第三方安卓浏览器
//       （SamsungBrowser / MiuiBrowser / UCBrowser / HuaweiBrowser / 无 "Chrome" 字样）
//       整条绕过它，首帧按「不是待定」画出按钮、事件到达后再翻掉 —— 这才是手机上那一下。

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

// 3) 「还没结论」与「不能装」必须分开：判定未落定前不画按钮，否则它会先凭空出现再消失。
//    这道门必须覆盖**所有**平台，不能只挂在 isAndroidChrome 上：
//    SamsungBrowser / MiuiBrowser / UCBrowser / HuaweiBrowser 以及任何不含 "Chrome"
//    字样的安卓 UA，isAndroidChrome 都为 false，会整条绕过该门 —— 真机上正是它们在闪。
assert.doesNotMatch(
  fileTree,
  /const installProbePending = isAndroidChrome && !installProbeDone;/,
  "installProbePending 不得只挂在 isAndroidChrome 上（第三方安卓浏览器会绕过它继续闪）",
);
assert.match(
  fileTree,
  /const expectsInstallPrompt = !isIOS && !isMacSafari;/,
  "「该平台会不会派发 beforeinstallprompt」须独立成变量，不能混进 UA 判断",
);
assert.match(
  fileTree,
  /const installProbePending =\s*\n?\s*expectsInstallPrompt && !installProbeDone && !isKnownInstalled && !isNativeApp;/,
  "应显式区分「判定中」与「不能装」，且覆盖所有会派发该事件的平台",
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

// 3b) 事件可能永远不来（iOS Safari、Chrome 启发式压住），必须有超时兜底落定，
//     否则「等判定」会退化成「永不显示」。
assert.match(
  service,
  /const PROBE_TIMEOUT_MS = \d+;/,
  "必须有一个判定兜底时限常量",
);
assert.match(
  service,
  /setTimeout\(\(\) => \{[\s\S]{0,160}probeTimer = null;[\s\S]{0,160}probed: true/,
  "超时后必须把 probed 置 true，否则事件不来时按钮永远不出现",
);

// 3c) 反过来，Safari 系永远不派发该事件，绝不能对它设门 —— 那是把闪烁换成迟到。
//     （回归：iOS 上按钮从 ~0.3s 推迟到 6.0s 才出现。）
assert.doesNotMatch(
  fileTree,
  /const installProbePending = !installProbeDone && !isKnownInstalled && !isNativeApp;/,
  "installProbePending 不得无条件设门（Safari 不派发该事件，会白白迟到一个超时周期）",
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
