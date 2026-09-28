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

test("面板上浮靠 transition 终态切换，不是只进不退的 animation", () => {
  // animation 只能播进场；关闭时整层卸载就是硬切 —— 闪烁的一半来源。
  assert.match(panel, /data-archived-panel="sheet"/);
  // 只做竖直位移。scale 是第二个方向的运动，叠上去读起来就是「在缩放」。
  assert.doesNotMatch(panel, /scale\(/, "不该再有缩放动画");
  assert.match(panel, /transform: isOpen \? "translateY\(0\)" : "translateY\(48px\)"/);
  assert.match(panel, /transition: `transform \$\{timing\}, opacity \$\{timing\}`/);
  assert.doesNotMatch(panel, /animation: "mindfs-archived-rise/);
  const css = read("src/index.css");
  assert.doesNotMatch(css, /mindfs-archived-rise/, "旧关键帧应已删");
});

test("进出共用一套时长和一条线性曲线", () => {
  // 两侧是同一串 CSS 的镜像。曲线取 linear：形态在这里是过度设计，而 linear
  // 天生自反（把时间轴对折，曲线与自己重合），不可能跑偏。
  assert.match(panel, /const TRANSITION_MS = \d\d\d/);
  assert.match(panel, /const TRANSITION_EASE = "linear"/);
  assert.match(panel, /const timing = `\$\{TRANSITION_MS\}ms \$\{TRANSITION_EASE\}`/);
  // 方向相关的分支不许回来（那会让两侧悄悄跑成两套）
  assert.doesNotMatch(panel, /OPEN_MS|CLOSE_MS|OPEN_EASE|CLOSE_EASE/);
  // 只解析时长声明，别全文搜数字 —— 曲线常量也会命中
  const durations = panel.match(/_MS = \d+/g) || [];
  assert.equal(durations.length, 1, "只应有一个时长常量");
  assert.ok(
    Number(durations[0].replace(/.*= /, "")) >= 200,
    `时长应 >= 200ms（再慢就又显得磨叽），实得 ${durations[0]}`,
  );
  // 遮罩和面板两条 transition 都得是这一串
  assert.match(panel, /transition: `opacity \$\{timing\}`/);
  assert.match(panel, /transition: `transform \$\{timing\}, opacity \$\{timing\}`/);
});

test("面板常驻 DOM：isOpen 直接当终态样式，没有两态和延迟卸载", () => {
  // 进场「几乎瞬间」的真正原因不是曲线，是进场起点不存在：早先拆成
  // entered + mounted，靠 rAF 隔一帧再 setEntered，浏览器不给这一帧时，
  // 挂载和切终态落在同一次绘制里，没有起点就没有 transition —— 面板直接「在」，
  // 而退场永远有起点，所以是「进场瞬间、退场慢慢」。这套两态 + 卸载定时器
  // 是那个 bug 本身，不能回来。
  // 注释里会正面提到这些被删掉的 API（「早先拆成 entered + mounted…」），
  // 所以先剥掉行注释再断言它们不在代码里。
  const code = panel.replace(/^\s*\/\/.*$/gm, "");
  assert.doesNotMatch(code, /useState/, "不再需要任何本组件自有的状态");
  assert.doesNotMatch(code, /setEntered|setMounted/, "两态开关已删");
  assert.doesNotMatch(code, /requestAnimationFrame/, "延迟进场就是那个 bug 的成因");
  assert.doesNotMatch(code, /setTimeout\(\(\) => setMounted/, "不再有延迟卸载");
  // 终态样式直接由 isOpen 决定
  assert.match(panel, /opacity: isOpen \? 1 : 0/);
  assert.match(panel, /transform: isOpen \? "translateY\(0\)" : "translateY\(48px\)"/);
  // 收起后仍占整列但不透明、不吃点击，把交互还给底下的列表
  assert.match(panel, /pointerEvents: isOpen \? "auto" : "none"/);
  // 挂载交给组件自己，hook 不再条件渲染
  assert.match(hook, /<ArchivedSessionsPanel\s*\n\s*isOpen=\{archiveOpen\}/);
  assert.doesNotMatch(hook, /\{archiveOpen \? \(/);
});

test("遮罩跟着淡入淡出（不是瞬间满不透明）", () => {
  assert.match(panel, /opacity: isOpen \? 1 : 0,\s*\n\s*transition: `opacity \$\{timing\}`/);
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
  // 只在展开时挂进返回栈：常驻 DOM 后，恒 true 会让退场途中也占着返回栈。
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

test("会话列表始终在归档层下面挂着", () => {
  // 浮层压不住列表：它盖的是自己那一层，列表是普通流内容（滚动位置/展开态不丢）。
  assert.match(hook, /\{sessionSidebar\}/);
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
