import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs";
import path from "node:path";

/**
 * 「已归档对话」视图的形状契约。
 *
 * 这条守的是一条**否定式**需求：归档不能做成全屏浮层。
 * 但「不遮挡主面板/左栏」和「要蒙一层半透明黑、透出底下的列表」并不矛盾 ——
 * 关键是遮罩与面板都用 absolute 钉在**右栏自己那根列**里（AppShell 的
 * rightStyle 带 position: relative），作用域只到右栏。
 * 所以这里断言「不是 fixed / 不按视口铺开」，同时断言遮罩和上浮都在。
 */

const read = (rel) =>
  fs.readFileSync(path.resolve(import.meta.dirname, "../", rel), "utf8");

const panel = read("src/components/ArchivedSessionsPanel.tsx");
const hook = read("src/app/useSessionSidebarView.tsx");

test("归档视图不做全屏浮层：遮罩/面板都是 absolute，不是 fixed", () => {
  assert.doesNotMatch(panel, /position: "fixed"/, "全视口浮层会把主面板和左栏一起盖住");
  assert.doesNotMatch(panel, /100vh/, "不得按视口高度铺开");
  // 遮罩和面板都靠 layer 定位，所以 zIndex 只需要相对彼此的两级
  assert.doesNotMatch(panel, /zIndex: [3-9]\d\d/, "不应有全屏级 zIndex");
});

test("遮罩是半透明黑，罩住右栏内容、透出底下的列表", () => {
  assert.match(panel, /data-archived-panel="scrim"/);
  assert.match(panel, /position: "absolute",\s*inset: 0,[\s\S]*?background: "rgba\(0, 0, 0, [\d.]+\)"/);
  // 点遮罩收起
  assert.match(panel, /data-archived-panel="scrim"\s*\n\s*onClick=\{onClose\}/);
});

test("面板上浮（关键帧在全局 css 里，不是临时内联）", () => {
  assert.match(panel, /data-archived-panel="sheet"/);
  assert.match(panel, /animation: "mindfs-archived-rise/);
  const css = read("src/index.css");
  assert.match(css, /@keyframes mindfs-archived-rise \{[\s\S]*?translateY\(/);
});

test("归档层 absolute 盖满右栏列，不是跟会话列表并排的 flex item", () => {
  // 写成 flex item（flex:1）会跟列表上下分栏 —— 就是「堆在原有会话面板上方」
  // 那个 bug。它必须 absolute inset:0，浮在列表上面。
  assert.match(panel, /data-archived-panel="layer"[\s\S]*?position: "absolute",\s*inset: 0/);
  assert.doesNotMatch(
    panel.slice(panel.indexOf('data-archived-panel="layer"'), panel.indexOf('data-archived-panel="scrim"')),
    /flex: 1/,
    "归档层不能再是 flex item",
  );
  // 定位上下文由 hook 里那层 relative + 占满整列的 wrapper 提供
  assert.match(hook, /position: "relative",\s*flex: 1,\s*minHeight: 0,[\s\S]*?overflow: "hidden"/);
  const shell = read("src/layout/AppShell.tsx");
  const rightStyle = shell.slice(
    shell.indexOf("const rightStyle"),
    shell.indexOf("const footerStyle"),
  );
  assert.match(rightStyle, /position: "relative"/, "右栏列必须是定位上下文");
});

test("收起按钮在标题栏里、朝右，不关掉整个右栏", () => {
  assert.match(panel, /data-archived-panel="header"/);
  assert.match(panel, /data-archived-panel="collapse"/);
  assert.match(panel, /aria-label=\{t\("sessionList\.archivedPanel\.collapse"\)\}/);
  assert.match(panel, /onClick=\{onClose\}/);
  // chevron 朝右（顶点 x 递增）：中点在右 = 3.5,8 → 8,12.5 → 12.5,8
  const chevron = panel.slice(panel.indexOf('data-archived-panel="collapse"'));
  assert.match(
    chevron,
    /<polyline points="3\.5 8 8 12\.5 12\.5 8" \/>/,
    "收起按钮应指向右",
  );
});

test("返回键/侧滑能退回会话列表", () => {
  // 不遮挡主面板 ≠ 不是一级视图：它占着右栏，得能退回去。
  // 挂载即打开，所以 useBackLayer 恒为 true。
  assert.match(panel, /useBackLayer\(true, onClose\)/);
});

test("归档视图不重复画一遍空操作栏", () => {
  // MultiProjectSessionList 顶部那排是搜索/导入入口，归档视图里用不上，
  // 视图自带标题栏，再画一条就是两条空头。
  const list = read("src/components/SessionList.tsx");
  assert.match(list, /hideHeader = false/);
  assert.match(list, /display: hideHeader \? "none" : "flex"/);
  assert.match(panel, /hideHeader/);
});

test("收起时归档层整体卸载，会话列表不受影响", () => {
  // 浮层压不住列表：它盖的是自己那一层，列表在下面继续挂着（滚动位置/展开态不丢）。
  assert.match(hook, /\{sessionSidebar\}[\s\S]*?\{archiveOpen \? \(\s*<ArchivedSessionsPanel/);
  assert.doesNotMatch(hook, /flex: archiveOpen \?/);
});

test("归档视图挂在右栏 hook 里，不在 App 根上", () => {
  assert.match(hook, /<ArchivedSessionsPanel/);
  const app = read("src/App.tsx");
  assert.doesNotMatch(app, /<ArchivedSessionsPanel/, "面板不得再挂回 App 根");
  assert.match(app, /rightSidebar=\{sessionSidebar\}/);
});

test("没有归档会话的项目不显示（空分组被丢掉）", () => {
  const app = read("src/App.tsx");
  const start = app.indexOf("// 归档视图懒加载");
  assert.ok(start >= 0, "App.tsx 应有归档懒加载 effect");
  const body = app.slice(start, app.indexOf("}, [archiveOpen, archiveReloadToken])", start));
  assert.match(body, /if \(sessions\.length === 0\) return null;/);
  assert.match(body, /\.filter\(\(g\): g is MultiProjectSessionGroup => !!g\)/);
  // 一条归档都没有时面板也不推空视图
  assert.match(panel, /groups\.length === 0|emptyText/);
});

test("归档按全部项目查，不按多项目分组（含全部归档的项目不能漏）", () => {
  // 会话被全部归档 → 该项目从多项目分组消失 → 按分组查会漏掉它的归档行，
  // 也就再也没法取消归档。必须按 managedRoot 全集查。
  const app = read("src/App.tsx");
  const start = app.indexOf("// 归档视图懒加载");
  const body = app.slice(start, app.indexOf("}, [archiveOpen, archiveReloadToken])", start));
  assert.match(body, /managedRootIdsRef\.current/);
  assert.doesNotMatch(body, /multiProjectSessionGroupsRef/);
});

test("面板复用主列表的项目分组，且不再有嵌套归档入口", () => {
  assert.match(panel, /<MultiProjectSessionList/);
  assert.doesNotMatch(panel, /onToggleArchive/);
  assert.doesNotMatch(panel, /archivedSessions=/);
});

test("侧栏底部只留入口行，归档内容不再内联展开", () => {
  const list = read("src/components/SessionList.tsx");
  assert.match(list, /data-archive-entry="open"/);
  assert.doesNotMatch(list, /function ArchiveSection/);
  assert.match(list, /onOpenArchivePanel/);
});

test("两种列表（单项目 / 多项目）都接了归档入口", () => {
  const list = read("src/components/SessionList.tsx");
  const entries =
    list.match(/<ArchiveEntryRow onOpen=\{onOpenArchivePanel\} \/>/g) || [];
  assert.equal(entries.length, 2, "SessionList 和 MultiProjectSessionList 各一处入口行");
});

test("面板文案两种语言都有", () => {
  for (const locale of ["zh-CN", "en-US"]) {
    const src = read(`src/i18n/locales/${locale}.ts`);
    assert.match(src, /"sessionList\.archivedPanel\.title":/);
    assert.match(src, /"sessionList\.archivedPanel\.collapse":/);
    assert.match(src, /"sessionList\.archivedPanel\.empty":/);
  }
});
