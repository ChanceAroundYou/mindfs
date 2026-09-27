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

// 覆盖层优先于视图栈：栈非空就先关最上面那一层。
assert.match(
  nav,
  /if \(hasBackLayer\(\)\) \{[\s\S]*?event\.preventDefault\(\);[\s\S]*?closeTopBackLayer\(\);/,
  "an open overlay must be closed before anything else, and the event must be cancelled",
);
// 无处可退时把控制权交回系统 —— 也就是允许退出。
assert.match(
  nav,
  /if \(onFallback\(\)\) \{\s*event\.preventDefault\(\);\s*\}/,
  "the fallback must be able to signal 'handled' so the view switch prevents app exit",
);
// 视图栈还有货时退视图，没有才返回 false。
assert.match(
  app,
  /const previous = popViewHistory\(\);\s*if \(!previous\) return false;/,
  "back should pop the previous view and report 'not handled' only when the stack is empty",
);
// 退视图后 URL 必须跟着更新，否则按钮高亮和实际视图不同源。
// 注意是「合并」而不是整体覆盖：只写 view 的话会把当前项目/文件抹掉。
assert.match(
  app,
  /switchMainView\(previous as MainViewMode\);[\s\S]*?const now = readURLState\(\);\s*replaceURLState\(\{ \.\.\.now, view: previous as MainViewMode \}\);/,
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
