import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs";
import path from "node:path";

/**
 * 「已归档对话」浮动面板的形状契约。
 *
 * 这几条全是**结构**断言（面板有没有遮罩、收起按钮接没接返回栈、列表里还有没有
 * 内联归档区）—— 它们守的是「重构时最容易悄悄弄丢」的部分，视觉细节交给 typecheck。
 */

const read = (rel) =>
  fs.readFileSync(path.resolve(import.meta.dirname, "../", rel), "utf8");

const panel = read("src/components/ArchivedSessionsPanel.tsx");

test("面板有遮罩，且遮罩点一下就收起", () => {
  assert.match(panel, /data-archived-panel="scrim"/);
  assert.match(panel, /background: "rgba\(0, 0, 0, 0\.32\)"/);
  assert.match(panel, /onClick=\{onClose\}/);
});

test("面板顶上留一块空白，不是满屏", () => {
  // 满屏会和主面板糊成一片，看不出这是浮层。
  assert.match(panel, /const TOP_GAP = \d+;/);
  assert.match(panel, /height: `calc\(100vh - \$\{TOP_GAP\}px\)`/);
});

test("收起按钮独立于遮罩存在", () => {
  // 遮罩是整片可点的，但用户不该靠猜才知道点哪能收 —— 要一个显式按钮。
  assert.match(panel, /data-archived-panel="collapse"/);
  assert.match(panel, /aria-label=\{t\("sessionList\.archivedPanel\.collapse"\)\}/);
});

test("面板接入返回栈（返回键 / 边缘侧滑能收起）", () => {
  assert.match(panel, /useBackLayer\(isOpen, onClose\)/);
});

test("面板复用主列表的项目分组，且不再有嵌套归档入口", () => {
  assert.match(panel, /<MultiProjectSessionList/);
  // 面板里再放一个归档入口会套娃
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
  const entries = list.match(/<ArchiveEntryRow onOpen=\{onOpenArchivePanel\} \/>/g) || [];
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

test("App.tsx 挂上面板，且不再往侧栏列表传归档数据", () => {
  const app = read("src/App.tsx");
  assert.match(app, /<ArchivedSessionsPanel/);
  assert.match(app, /isOpen=\{archiveOpen\}/);
  // 归档数据只活在面板里，不该再流回侧栏
  assert.doesNotMatch(app, /archivedSessions=\{archivedSessions\}/);
  assert.match(app, /groups=\{archivedGroups\}/);
});
