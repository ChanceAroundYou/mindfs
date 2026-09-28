import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs";
import path from "node:path";

/**
 * 「已归档对话」视图的形状契约。
 *
 * 这条守的是一条**否定式**需求：归档只能在右栏列里换视图，不能做成全屏浮层。
 * 前一版就是挂在 App 根上的 position:fixed 遮罩，把主面板和左栏一起盖了 ——
 * 所以这里显式断言「没有 fixed / 没有遮罩」，防止有人再改回去。
 *
 * 面板本身零定位、零遮挡：它是右栏 flex 列里的一个普通兄弟节点，
 * 高度切换和侧栏动画都交给 AppShell 那一层。
 */

const read = (rel) =>
  fs.readFileSync(path.resolve(import.meta.dirname, "../", rel), "utf8");

const panel = read("src/components/ArchivedSessionsPanel.tsx");
const hook = read("src/app/useSessionSidebarView.tsx");

test("归档视图不做全屏浮层：没有 fixed 定位、没有遮罩", () => {
  assert.doesNotMatch(panel, /position: "fixed"/, "归档视图不得脱离右栏列");
  assert.doesNotMatch(panel, /data-archived-panel="scrim"/, "不得有遮罩盖住主面板/左栏");
  assert.doesNotMatch(panel, /100vh/, "不得按视口高度铺开");
  assert.doesNotMatch(panel, /zIndex/, "右栏内部不需要再叠一层");
});

test("归档视图是右栏 flex 列里的普通子节点", () => {
  assert.match(panel, /flex: 1,\s*minHeight: 0,\s*display: "flex"/);
  assert.match(panel, /data-archived-panel="sheet"/);
});

test("收起按钮在标题栏里（不是关掉整个右栏）", () => {
  assert.match(panel, /data-archived-panel="header"/);
  assert.match(panel, /data-archived-panel="collapse"/);
  assert.match(panel, /aria-label=\{t\("sessionList\.archivedPanel\.collapse"\)\}/);
  assert.match(panel, /onClick=\{onClose\}/);
});

test("返回键/侧滑能退回会话列表", () => {
  // 不遮挡主面板 ≠ 不是一级视图：它占着右栏，得能退回去。
  assert.match(panel, /useBackLayer\(isOpen, onClose\)/);
});

test("归档视图不重复画一遍空操作栏", () => {
  // MultiProjectSessionList 顶部那排是搜索/导入入口，归档视图里用不上，
  // 视图自带标题栏，再画一条就是两条空头。
  const list = read("src/components/SessionList.tsx");
  assert.match(list, /hideHeader = false/);
  assert.match(list, /display: hideHeader \? "none" : "flex"/);
  assert.match(panel, /hideHeader/);
});

test("右栏里归档视图与会话列表按 flex 高度互换，而不是叠着", () => {
  // 叠着（两个都 flex:1）的话归档会盖在列表上面；用 0/100% 才是真正的「换视图」。
  assert.match(hook, /flex: archiveOpen \? "1 1 100%" : "0 1 0%"/);
  assert.match(hook, /flex: archiveOpen \? "0 1 0%" : "1 1 100%"/);
  // 收起后列表仍然挂载：滚动位置和展开态要留着
  assert.match(hook, /\{sessionSidebar\}/);
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
  // 一条归档都没有时不推空视图
  assert.match(panel, /groups\.length === 0/);
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
