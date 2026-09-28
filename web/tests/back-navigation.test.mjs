import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// 「返回上一级」的契约。以前这条链整根是断的：
//   - Android 硬件返回键派发了 mindfs:android-back-request，但全项目零监听；
//   - switchMainView 全程 replaceState，历史栈永远只有一条；
//   - html 的 overscroll-behavior: none 关掉了 iOS 的边缘侧滑。
// 表现就是「侧滑/后退直接退出」。这些断言钉住修复后的行为。
const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const main = readFileSync(new URL("../src/main.tsx", import.meta.url), "utf8");
const nav = readFileSync(new URL("../src/app/useBackNavigation.ts", import.meta.url), "utf8");
const dialogHost = readFileSync(new URL("../src/components/DialogHost.tsx", import.meta.url), "utf8");
const css = readFileSync(new URL("../src/index.css", import.meta.url), "utf8");

// —— 分发器本身 ——

// —— 切视图必须真的压一条历史 ——
// 这是「侧滑还是直接退出」的直接原因：以前 switchMainView 全程 replaceState，
// 浏览器历史永远只有一项，popstate 压根不触发，侧滑把应用直接带出去。
// 覆层关掉之后能退视图，靠的就是这条 push 出来的记录。
// pushState 住在 useBackNavigation.ts：它要同时维护计数器（Android 返回键靠它
// 判断有没有得退），放在 App.tsx 的 useCallback 里就得回传，徒增一层。
assert.match(
  nav,
  /export function pushViewHistoryEntry\(from: string\): void \{\s*window\.history\.pushState\(\{ mindfsView: from \}/,
  "switching views must push a real history entry, or back has nothing to pop",
);
assert.match(
  app,
  /pushViewHistoryEntry\(mainViewRef\.current\);\s*switchMainView\(mode\);/,
  "the switcher must record the view it is leaving before switching",
);
// popstate 认这条记录：判据是 state 里的视图，不能走 handlePopState ——
// push 存的是切换瞬间的 URL，view 字段可能还没写进去。
assert.match(
  app,
  /const fromView = \(event\.state as \{ mindfsView\?: MainViewMode \} \| null\)\?\.mindfsView;/,
  "popstate must recognise the view-switch entry by its state, not by parsing the URL",
);
assert.match(
  app,
  /if \(fromView\) \{[\s\S]*?consumeViewHistoryEntry\(\);\s*mainViewRef\.current = fromView;\s*setMainView\(fromView\);/,
  "popping a view-switch entry must go back to that view and keep the counter in sync",
);

// —— 单一机制：不能有第二份「视图栈」 ——
// 这条是真实 bug：曾经一边 pushState、一边往一个 viewHistory 数组里 push，
// 一次切换记两条、返回一次只弹一条，用户看着就是「返回要按两下」。
// 现在只有浏览器历史，数组整套删掉，只剩一个计数器。
assert.doesNotMatch(
  nav,
  /const viewHistory: string\[\] = \[\]/,
  "there must be no parallel view-history array — the browser history is the only stack",
);
assert.doesNotMatch(
  nav,
  /export function popViewHistory|export function pushViewHistory\b|export function hasViewHistory\(\)/,
  "the parallel array's push/pop/has trio is gone",
);
// 计数器只归这对函数动，push 加一、pop 减一。
assert.match(
  nav,
  /export function consumeViewHistoryEntry\(\): void \{\s*if \(pendingViewEntries > 0\) pendingViewEntries -= 1;/,
  "popstate must decrement the counter, and clamp at zero",
);
// Android 返回键汇进同一条路：走 history.back()，不再自己消费一层/一视图。
// 两套机制各走一遍正是「返回要按两下」的另一半原因。
assert.match(
  nav,
  /export function installBackNavigation\(\): \(\) => void \{[\s\S]*?window\.history\.back\(\);/,
  "the android back key must go through history.back() so it shares one path with swipe/browser back",
);
assert.doesNotMatch(
  nav,
  /installBackNavigation\(onFallback/,
  "installBackNavigation must not take a second, parallel back route",
);
// 覆盖层优先于视图栈：层非空就先关最上面那一层。
// 两条分支都要 preventDefault，否则 Capacitor 默认退出照做，表现为
// 「先关掉面板、紧接着整个应用退出」。
assert.match(
  nav,
  /if \(hasBackLayer\(\)\) \{[\s\S]{0,400}?event\.preventDefault\(\);/,
  "an open overlay must be closed first, and the event must be cancelled",
);
assert.match(
  nav,
  /if \(hasViewHistoryEntry\(\)\) \{\s*event\.preventDefault\(\);\s*window\.history\.back\(\);/,
  "with no overlay, a pending view entry must go through back() and cancel the exit",
);
// 覆盖层开着但没有视图历史时必须**直接关层**，不能走 back()：
// 那条路只在视图切换时压过记录，冷启动直接开面板时 back() 是空操作、popstate 不触发，
// 面板就卡住关不掉，退出又被 preventDefault 挡死。
assert.match(
  nav,
  /if \(hasBackLayer\(\)\) \{[\s\S]{0,400}?hasViewHistoryEntry\(\)[\s\S]{0,80}?window\.history\.back\(\);[\s\S]{0,120}?\} else \{[\s\S]{0,80}?closeTopBackLayer\(\);/,
  "with an overlay open and no view history, close the layer directly — back() would be a no-op and trap the panel",
);
assert.match(
  app,
  /if \(hasBackLayer\(\)\) \{[\s\S]*?window\.history\.pushState\([\s\S]*?closeTopBackLayer\(\);\s*return;/,
  "popstate with an open overlay must close only that overlay and push the entry back",
);
// 退视图后 URL 必须跟着更新，否则按钮高亮和实际视图不同源。
// 注意是「合并」而不是整体覆盖：只写 view 的话会把当前项目/文件抹掉。
assert.match(
  app,
  /const now = readURLState\(\);\s*replaceURLState\(\{ \.\.\.now, view: fromView \}\);/,
  "going back a view must keep the URL in sync with the switcher without dropping root/file/session",
);

// —— 接了哪些层 ——

// 三个 PanelShell/对话框 + 悬浮框。少接一个，那个面板的返回键就还是退出。
for (const [name, pattern] of [
  ["scheduled agent dialog", /useBackLayer\(scheduledAgentDialogOpen,/],
  ["task template dialog", /useBackLayer\(taskTemplateDialogOpen,/],
  ["new-task panel", /useBackLayer\(!!taskInlineEdit,/],
  ["files-view drawer", /useBackLayer\(isDrawerOpen && mainView === "files",/],
]) {
  assert.match(app, pattern, `the ${name} must be reachable by back`);
}
// 保存中不给关：和面板上那个 × 同一条规则，否则返回键会绕开保存锁。
assert.match(
  app,
  /useBackLayer\(!!taskInlineEdit, \(\) => \{\s*\/\/ 保存中不给关[\s\S]*?if \(!taskInlineSaving\) closeTaskEditDialog\(\);/,
  "back must not close the new-task panel while it is saving",
);
// 弹窗是最高的一层，走 dismiss（取消语义），不穿透到面板。
assert.match(
  dialogHost,
  /useBackLayer\(!!request, \(\) => dialogService\.dismiss\(\)\);/,
  "the dialog must be the topmost back layer and dismiss via its own cancel semantics",
);

// —— iOS 边缘侧滑 ——

// 事件必须可取消：不可取消时 preventDefault 静默失效，
// 按返回会「先关一层、紧接着整个应用退出」。
assert.match(
  main,
  /new CustomEvent\("mindfs:android-back-request", \{ cancelable: true \}\)/,
  "the android back event must be cancelable or preventDefault silently no-ops",
);
// html 只锁纵向。两轴全锁时 iOS 的边缘返回手势压根不触发。
// 剥注释再断言 —— 注释里得留着「为什么改」，否则下一个人会改回去。
const cssDeclarations = css.replace(/\/\*[\s\S]*?\*\//g, "");
assert.doesNotMatch(
  cssDeclarations,
  /overscroll-behavior:\s*none/,
  "html must not lock both axes — that disables the iOS edge-swipe back gesture",
);
assert.match(
  css,
  /html \{[\s\S]*?overscroll-behavior-y: none;/,
  "html should keep locking the vertical axis (rubber-band white gap) while leaving horizontal free",
);

// 浏览器后退/iOS 侧滑走 popstate：覆盖层开着时只关那一层，
// 并把刚被弹掉的条目 push 回去，否则下一次后退直接退到站外。
assert.match(
  app,
  /if \(hasBackLayer\(\)\) \{[\s\S]*?window\.history\.pushState\([\s\S]*?closeTopBackLayer\(\);\s*return;/,
  "popstate with an open overlay must close only that overlay and push the entry back",
);

console.log("back-navigation.test.mjs: OK");
