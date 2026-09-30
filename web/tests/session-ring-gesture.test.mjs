import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

// 蓝环的手势接线：SessionRing 必须真的走 quickSwitch 的 ringGesture，
// 而不是自己在组件里另写一套阈值。
//
// 背景：上游把「左滑新建 / 上滑快捷切换」抽成 services/quickSwitch.ts 的
// ringGesture，本地合并时改成把它嫁接进自己的 SessionRing（本地环另有
// 主题色、抽屉感知、点按行为，不能整套换回上游组件）。这个测试守住
// 「嫁接」这件事——ringGesture 一旦被组件内联或绕过，两个方向就静默失效，
// 而 UI 上看不出任何异常。

const srcRoot = path.resolve(import.meta.dirname, "../src");
const ring = fs.readFileSync(path.join(srcRoot, "components/action/SessionRing.tsx"), "utf8");

// 1. 组件从 services/quickSwitch 取判定，不自己写常量阈值
assert.match(
  ring,
  /from "\.\.\/\.\.\/services\/quickSwitch"/,
  "SessionRing must import the shared gesture helper",
);
assert.doesNotMatch(
  ring,
  /DRAG_THRESHOLD\s*=/,
  "SessionRing must not re-declare its own drag threshold; ringGesture owns that",
);

// 2. 拖拽结束统一交给 ringGesture，且两个方向都接了。
//    断言要卡在 finishDrag 的函数体上：只查 ringGesture 出现过是不够的——
//    组件里可以留一处调用、同时在别处另写阈值，那种情况上一版测试就是绿的。
const finishDrag = ring.match(/const finishDrag = useCallback\(\(\) => \{[\s\S]*?\n  \}, \[/);
assert.ok(finishDrag, "SessionRing must keep a single finishDrag callback");
const finishBody = finishDrag[0];
assert.match(finishBody, /ringGesture\(dragX, dragY\)/, "finishDrag must decide the gesture via ringGesture");
assert.match(finishBody, /action === "new"/, "left drag must still start a new session");
assert.match(finishBody, /action === "switch"/, "up drag must open the quick switch panel");
// 阈值只能来自 ringGesture：finishDrag 里不得出现裸数字阈值
assert.doesNotMatch(
  finishBody,
  /[<>]=?\s*-?\d+/,
  "finishDrag must not carry its own pixel threshold; ringGesture owns that",
);

// 3. 快捷切换面板的回调链贯通到 App
assert.match(ring, /onSelectProject\?\.?\(group\.rootId\)|onSelectProject\(group\.rootId\)/, "panel must select a project");
assert.match(ring, /onSelectSession\?/, "panel must select a session");
assert.doesNotMatch(ring, /window\.prompt/, "quick switch must not fall back to a system dialog");

// 4. ActionBar 把回调透传给环，App 提供实现
const actionBar = fs.readFileSync(path.join(srcRoot, "components/ActionBar.tsx"), "utf8");
assert.match(actionBar, /onSelectProject=\{onSelectProject\}/, "ActionBar must forward onSelectProject to SessionRing");
assert.match(actionBar, /onSelectSession=\{onSelectSession\}/, "ActionBar must forward onSelectSession to SessionRing");

const app = fs.readFileSync(path.join(srcRoot, "App.tsx"), "utf8");
assert.match(app, /onSelectProject=\{\(root\) =>/, "App must supply a project picker");
assert.match(app, /onSelectSession=\{\(session\) =>/, "App must supply a session picker");

// 5. 上游那套独立组件已被本地环取代，不该并存
assert.equal(
  fs.existsSync(path.join(srcRoot, "components/SessionQuickActions.tsx")),
  false,
  "SessionQuickActions.tsx must stay deleted; its gesture lives in SessionRing now",
);
