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
 *
 * **只钉行为，不钉取值。** 面板沉下 48px 还是 56px、缓动走 linear 还是 spring、
 * 遮罩黑到 0.42 还是 0.5、收起按钮的 chevron 画多长 —— 改了都不该让这里变红，
 * 那些是调参不是 bug。凡是「换个数/换个曲线就红」的断言都已经删掉了。
 *
 * 反过来，凡是「改坏了会重演某个旧 bug」的都留着，且每条都注明那个 bug。
 */

const read = (rel) =>
  fs.readFileSync(path.resolve(import.meta.dirname, "../", rel), "utf8");

const panel = read("src/components/ArchivedSessionsPanel.tsx");
const hook = read("src/app/useSessionSidebarView.tsx");

test("归档视图不做全屏浮层", () => {
  assert.doesNotMatch(panel, /position: "fixed"/, "全视口浮层会把主面板和左栏一起盖住");
  assert.doesNotMatch(panel, /100vh/, "不得按视口高度铺开");
});

test("遮罩罩住右栏、半透明、点它收起、跟着淡入淡出", () => {
  assert.match(panel, /data-archived-panel="scrim"/);
  const at = panel.indexOf('data-archived-panel="scrim"');
  const body = panel.slice(at, panel.indexOf("/>", at));
  assert.match(body, /position: "absolute",\s*inset: 0/, "遮罩必须铺满右栏整列");
  // 「半透明黑」是行为，黑到多少是小节。alpha 必须落在 (0,1)：
  // 1 会把底下的会话列表盖死，0 等于没画。
  const alpha = body.match(/background: "rgba\(\s*0,\s*0,\s*0,\s*([\d.]+)\s*\)"/);
  assert.ok(alpha, "遮罩底色应为黑色 rgba");
  assert.ok(
    Number(alpha[1]) > 0 && Number(alpha[1]) < 1,
    `遮罩应半透明（alpha 落在 0~1 之间），实得 ${alpha[1]}`,
  );
  assert.match(body, /onClick=\{onClose\}/, "点遮罩收起");
  // 不透明度跟着 isOpen 走才谈得上「淡入淡出」；瞬时满不透明是闪烁的一半来源。
  assert.match(body, /opacity: isOpen \? 1 : 0/, "遮罩要跟着开合淡入淡出");
});

test("面板靠 transition 在终态之间移动，不是只进不退的 animation", () => {
  // animation 只能播进场；关闭时整层卸载就是硬切 —— 闪烁的一半来源。
  assert.match(panel, /data-archived-panel="sheet"/);
  // 只做竖直位移。scale 是第二个方向的运动，叠上去读起来就是「在缩放」。
  assert.doesNotMatch(panel, /scale\(/, "不该再有缩放动画");
  const at = panel.indexOf('data-archived-panel="sheet"');
  const body = panel.slice(at, panel.indexOf(">", at));
  // 开合两个终态都必须存在**且不同**：关闭态若与开启态同值，退场就没有起点，
  // 又回到「进场瞬间、退场才有过渡」。具体沉多少 px 不管。
  const transform = body.match(
    /transform: isOpen \? "translateY\(([^)]*)\)" : "translateY\(([^)]*)\)"/,
  );
  assert.ok(transform, "sheet 的 transform 应由 isOpen 决定两个 translateY 终态");
  assert.notEqual(transform[1], transform[2], "关闭态与开启态位移相同 = 退场没有起点");
  // 旧的单向关键帧不该回来
  assert.doesNotMatch(panel, /animation: "mindfs-archived-rise/);
  assert.doesNotMatch(read("src/index.css"), /mindfs-archived-rise/, "旧关键帧应已删");
});

test("进出共用一套时长，没有方向分支", () => {
  // 方向相关的分支不许回来：进、出各一套时长就会悄悄跑偏 —— 一侧改了另一侧忘了改。
  assert.doesNotMatch(panel, /OPEN_MS|CLOSE_MS|OPEN_EASE|CLOSE_EASE/);
  // 时长常量只能有一个。曲线取什么值不管（linear / ease / spring 都是调参）。
  const durations = panel.match(/_MS = \d+/g) || [];
  assert.equal(durations.length, 1, `只应有一个时长常量，实得 ${durations.join(", ")}`);
  // 遮罩与面板各一条 transition，且都走同一个 timing 串 —— 这才叫「共用一套」。
  // 只钉「引用了同一个变量」，不钉那串长什么样。
  assert.equal(
    (panel.match(/transition: `[^`]*\$\{timing\}`/g) || []).length,
    2,
    "遮罩与面板各一条 transition，且都引用同一个 timing",
  );
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
  assert.doesNotMatch(code, /requestAnimationFrame/, "延迟进场就是那个 bug 的成因");
  assert.doesNotMatch(code, /setTimeout/, "不再有延迟挂载/卸载");
  assert.doesNotMatch(code, /setEntered|setMounted/, "两态开关已删");
  // 终态样式直接由 isOpen 决定
  assert.match(panel, /opacity: isOpen \? 1 : 0/);
  // 收起后仍占整列但不透明、不吃点击，把交互还给底下的列表
  assert.match(panel, /pointerEvents: isOpen \? "auto" : "none"/);
  // 挂载交给组件自己，hook 不再条件渲染
  assert.match(hook, /<ArchivedSessionsPanel\s*\n\s*isOpen=\{archiveOpen\}/);
  assert.doesNotMatch(hook, /\{archiveOpen \? \(/);
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
  // 定位上下文与裁剪由 hook 里那层 wrapper 提供：没有 relative，inset:0 会一路
  // 找到视口（退化成全屏浮层）；没有 overflow:hidden，上浮的面板会溢出右栏。
  // 只取外层那个开标签 —— 内层还包着一层同样带 overflow:hidden 的 div，
  // 按整段切会把它的样式误当成外层的（那样「删掉外层的裁剪」测不出来）。
  const view = hook.slice(hook.indexOf("const archiveView = ("));
  const openTag = view.slice(0, view.indexOf(">", view.indexOf("style={{")));
  assert.match(openTag, /position: "relative"/, "wrapper 必须是定位上下文");
  assert.match(openTag, /overflow: "hidden"/, "wrapper 必须裁剪，否则面板溢出右栏");
  const shell = read("src/layout/AppShell.tsx");
  const rightStyle = shell.slice(
    shell.indexOf("const rightStyle"),
    shell.indexOf("const footerStyle"),
  );
  assert.match(rightStyle, /position: "relative"/, "右栏列必须是定位上下文");
});

test("收起按钮在标题栏里，点它是退回会话列表、不关掉整个右栏", () => {
  assert.match(panel, /data-archived-panel="header"/);
  assert.match(panel, /data-archived-panel="collapse"/);
  assert.match(panel, /aria-label=\{t\("sessionList\.archivedPanel\.collapse"\)\}/);
  const at = panel.indexOf('data-archived-panel="collapse"');
  const button = panel.slice(at, panel.indexOf("</button>", at));
  assert.match(button, /onClick=\{onClose\}/);
  // 只钉「有图标」；chevron 画成什么样、朝哪边是设计取值，不在这钉死。
  assert.match(button, /<svg/, "收起按钮要有图标");
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

test("归档入口在顶栏：搜索图标右边，只留图标且与搜索同尺寸", () => {
  // 2026-09-29 用户要求：归档入口从列表末尾挪到顶栏、搜索图标右边，去掉文字只留图标。
  // 原来挂在列表里，吸不吸底都跟着项目列表跑，得滚动才看得到。
  const list = read("src/components/SessionList.tsx");
  const fn = list.slice(list.indexOf("function ArchiveHeaderButton({"));
  const body = fn.slice(0, fn.indexOf("\n}\n"));
  assert.match(body, /data-archive-entry="open"/, "入口仍需带这个标记供定位");
  // 尺寸必须与搜索按钮一致，否则两个图标并排会一高一低。
  // 比的是「两者相等」而不是「都是 34px」：具体尺寸是设计调参，一致性才是行为。
  const dims = (src) =>
    ["width", "height", "minWidth"].map(
      (prop) => (src.match(new RegExp(`${prop}: "(\\d+)px"`)) || [])[1],
    );
  const searchAt = list.indexOf("onClick={onSearchToggle}");
  const searchBtn = list.slice(searchAt, list.indexOf("</button>", searchAt));
  assert.deepEqual(dims(body), dims(searchBtn), "归档入口必须与搜索按钮同尺寸");
  // 只留图标：文字只能走 aria-label / title，不能作为可见内容
  assert.match(body, /aria-label=\{t\("sessionList\.archive"\)\}/);
  assert.match(body, /title=\{t\("sessionList\.archive"\)\}/);
  assert.doesNotMatch(body, />\s*\{?t\("sessionList\.archive"\)\}?\s*</, "按钮内不得有可见文字");

  // 列表末尾的入口行必须彻底消失：它挂在滚动容器里，会被内容顶走、
  // 也会随「有无会话」分支消失。
  assert.doesNotMatch(list, /ArchiveEntryRow/, "列表末尾的归档行应已移除");
  // 两处接线都必须落在各自列表的顶栏里（各自第一个滚动容器之前）。
  // 不能按 data-archive-entry 找使用点 —— 那个标记只在组件定义里出现一次，
  // 拿它去「滚动容器窗口里搜」永远搜不到，等于没测。
  const split = list.indexOf("export function MultiProjectSessionList");
  for (const [chunk, label] of [
    [list.slice(0, split), "SessionList"],
    [list.slice(split), "MultiProjectSessionList"],
  ]) {
    const at = chunk.indexOf("<ArchiveHeaderButton");
    const scroller = chunk.indexOf('overflow: "auto"');
    assert.ok(at >= 0, `${label} 应接入归档入口`);
    assert.ok(
      scroller === -1 || at < scroller,
      `${label}: 归档入口必须在顶栏，不能在滚动内容里（否则得滚到列表末尾才看得到）`,
    );
  }
});

test("顶栏归档入口必须在搜索三元分支之外，两种列表都接了", () => {
  const list = read("src/components/SessionList.tsx");
  // 搜索结果态下顶栏是返回箭头，归档入口若放进那个三元分支会随搜索一起消失。
  const entries = list.match(/<ArchiveHeaderButton onOpen=\{onOpenArchivePanel\} \/>/g) || [];
  assert.equal(entries.length, 2, "SessionList 和 MultiProjectSessionList 各接一处");
  for (const at of entries) {
    const idx = list.indexOf(at);
    const before = list.slice(Math.max(0, idx - 2600), idx);
    // 该位置之前必须已经闭合过 searchResultsMode 三元分支
    assert.ok(before.includes(")}"), "归档入口应排在搜索三元分支之外");
  }
});

test("归档入口与搜索按钮同属一个分组：顶栏 space-between 不会把它推到中间", () => {
  // 顶栏是 justify-content: space-between，每个直接子元素各占一格均分。
  // 归档入口若自成一路（自带外层 div 或排在搜索组外），就会被甩到正中间
  // —— 实测离放大镜 117px。必须嵌进搜索按钮所在的那个 div 才能紧贴。
  const list = read("src/components/SessionList.tsx");
  const uses = [...list.matchAll(/<ArchiveHeaderButton onOpen=\{onOpenArchivePanel\} \/>/g)];
  assert.equal(uses.length, 2, "两个列表各接一处");
  for (const use of uses) {
    const WINDOW = 1800; // 搜索按钮连 SVG path 有千余字符
    // 必须在 onSearchToggle 那个搜索按钮的闭合之后、其父 div 闭合之前
    assert.ok(
      /onSearchToggle[\s\S]*<\/button>[\s\S]{0,200}ArchiveHeaderButton/.test(list.slice(Math.max(0, use.index - WINDOW), use.index + 60)),
      "归档入口应紧跟在搜索按钮之后、同属其父 div",
    );
  }
  // 组件本身不得再自带外层包装（那会让它脱离搜索分组）
  const fn = list.slice(list.indexOf("function ArchiveHeaderButton({"));
  const body = fn.slice(0, fn.indexOf("\n}\n"));
  assert.doesNotMatch(body, /<div[^>]*>\s*<button/, "按钮外不应再包一层 div");
});

test("面板文案两种语言都有", () => {
  for (const locale of ["zh-CN", "en-US"]) {
    const src = read(`src/i18n/locales/${locale}.ts`);
    assert.match(src, /"sessionList\.archivedPanel\.title":/);
    assert.match(src, /"sessionList\.archivedPanel\.collapse":/);
    assert.match(src, /"sessionList\.archivedPanel\.empty":/);
  }
});

// 这条守的是「子会话能展开、却收不回来」那个 bug 的根因，不只是现象。
test("展开的子会话能收回去：ToggleRowButton 不再套嵌套 button", () => {
  const list = read("src/components/SessionList.tsx");
  const fn = list.slice(list.indexOf("function ToggleRowButton({"));
  const body = fn.slice(0, fn.indexOf("\n}\n"));
  const code = body.replace(/^\s*\/\/.*$/gm, "");

  // 收起图标之前是个 button 里套 button（role="button" 的 span + stopPropagation）。
  // 嵌套 button 是非法 HTML，浏览器把子元素从父 button 里拆出来，事件到不了父级，
  // 那个图标就永远按不动 —— 表现正是「只能继续展开」。
  assert.doesNotMatch(code, /<button[\s\S]*<button/, "按钮里不能再套按钮");
  assert.doesNotMatch(code, /stopPropagation/, "不再需要靠阻止冒泡来隔离子按钮");
  assert.doesNotMatch(code, /role="button"/, "行内不再有第二个可点元素");

  // 收起 = 整行换 onCollapse，所以整行点得动
  assert.match(code, /onClick=\{collapsed \? onCollapse : onClick\}/);
  // 收起图标无条件跟着展开态走；「还有下一批」不再决定它出不出现
  assert.match(code, /\{showCollapseIcon \? icon\(true\) : null\}/);
});

test("图标只看展开态：还有下一批时不再把收起图标换成展开图标", () => {
  // `(!expanded || hasMore)` 会让「行文字写着收起、图标却是 ▾、点下去是加载更多」，
  // 三者互相矛盾，用户既看不到也点不到收回去。
  const list = read("src/components/SessionList.tsx");
  const code = list.replace(/^\s*\/\/.*$/gm, "");
  assert.doesNotMatch(code, /showExpandIcon=\{[^}]*\|\|[^}]*\}/, "展开图标不该被 hasMore 掺和");
  assert.doesNotMatch(code, /showCollapseIcon=\{[^}]*\|\|[^}]*\}/, "收起图标不该被 hasMore 掺和");
  assert.match(list, /showExpandIcon=\{!loading && !row\.expanded\}/);
  assert.match(list, /showExpandIcon=\{!loadingChild && !row\.expanded\}/);
  assert.match(list, /showExpandIcon=\{!projectLoading && !expanded\}/);
});

test("只有行内收起，没有顶部「全部收起」兜底行", () => {
  // 2026-09-28 用户明确要求删掉「全部收起」：它收的是**项目分组**的展开态，
  // 收不了子会话 —— 名字骗人，做的事又不是子会话。子会话靠行内那一行的收起图标。
  // （前一版我把它加回来也是错的，被同一轮反馈推翻了。）
  const list = read("src/components/SessionList.tsx");
  const code = list.replace(/^\s*\/\/.*$/gm, "");
  assert.doesNotMatch(code, /function CollapseAllRow/, "兜底行组件要删掉");
  assert.doesNotMatch(code, /data-collapse-all/, "不得再有 data-collapse-all 标记");
  assert.doesNotMatch(code, /const anyExpanded/, "不再有为此存在的聚合判定");
  assert.doesNotMatch(code, /const collapseAll = /, "不再有 collapseAll");
  for (const locale of ["zh-CN", "en-US"]) {
    assert.doesNotMatch(
      read(`src/i18n/locales/${locale}.ts`),
      /collapseAllChildren/,
      "i18n 里的兜底文案应一并删掉",
    );
  }
  // 行内收起仍然在：两处子会话行 + 一处项目分组行
  const wired = code.match(/onCollapse=/g) || [];
  assert.equal(wired.length, 3, "子会话行两处 + 项目分组行一处");
  assert.match(code, /onCollapse=\{\(\) => handleProjectCollapse\(group\)\}/);
});

test("收起后的「还有 N 个」按展开**那一刻**的已加载数算，不含展开时新拉回的批次", () => {
  // 用户报的 bug：折叠时是「还有 2 个子会话」，展开后点收起变成「还有 81 个」，
  // 也就是 b > a。根因是 hiddenCount 按 children.length 算，而展开会再拉一批
  // （每批最多 50 条）合并进 children，总数只增不减 —— 收起后的数字必然大于展开前。
  // 修法：基数在**展开前**钉进 collapsedBaseCount。反过来（收起时钉）会把 bug
  // 固化 —— 收起那一刻 sessions 里已经含新拉回的批次，钉进去的正是那个虚高的数。
  const list = read("src/components/SessionList.tsx");
  const code = list.replace(/^\s*\/\/.*$/gm, "");
  // 两个列表各一份 collapsedBaseCount state
  const states = code.match(/const \[collapsedBaseCount, setCollapsedBaseCount\]/g) || [];
  assert.equal(states.length, 2, "SessionList 和 MultiProjectSessionList 各一份");
  // 折叠计数必须读这份 state，不能直接用 children.length
  const reads = code.match(/const collapsedBase = collapsedBaseCount\[([^\]]+)\] \?\? children\.length;/g) || [];
  assert.equal(reads.length, 2, "两处 buildRows 都要按折叠基数算 hiddenCount");
  // 不得再有「按总数算」的旧写法
  assert.doesNotMatch(
    code,
    /const hiddenCount = Math\.max\(0, children\.length - COLLAPSED_CHILD_SESSION_LIMIT\)/,
    "这就是 b > a 的那行：按总数算",
  );
  // 钉基数只发生在两处展开分支（各一次），收起路径一处都不许有
  const pins = code.match(/setCollapsedBaseCount\(/g) || [];
  assert.equal(pins.length, 2, `只在两处展开分支钉基数，实得 ${pins.length}`);
  for (const fn of [...code.matchAll(/const handleChildToggle = async \([\s\S]*?\n  \};/g)].map((m) => m[0])) {
    const expandAt = fn.indexOf("!row.expanded");
    const pinAt = fn.indexOf("setCollapsedBaseCount(");
    assert.ok(pinAt > expandAt, "钉基数必须在展开分支（!row.expanded）里");
    const tail = fn.slice(fn.indexOf("else {", expandAt));
    assert.doesNotMatch(tail, /setCollapsedBaseCount\(/, "收起分支不得再钉基数");
  }
  // onCollapse（整行点 = 收起）只翻展开态，不碰基数。
  // 先剥掉行注释再匹配；剥完会留下空行，所以别指望原来的缩进 —— 直接从
  // onCollapse 切到下一个 `}` 就拿到函数体了。
  const stripped = code.replace(/^\s*\/\/.*$/gm, "");
  const onCollapse = [...stripped.matchAll(/onCollapse=\{\(\) =>/g)]
    .filter((m) => !stripped.slice(m.index, m.index + 60).includes("handleProjectCollapse"))
    .map((m) => stripped.slice(m.index, stripped.indexOf("}", m.index)));
  assert.equal(onCollapse.length, 2, "两处子会话行都有 onCollapse");
  for (const body of onCollapse) {
    assert.match(body, /setExpandedChildren\(/, "onCollapse 应翻展开态");
    assert.doesNotMatch(body, /setCollapsedBaseCount\(/, "收起时不得钉基数");
  }
});

test("buildRows 的读键与写键用同一个 childStateKey 调用形状（含 nodeId）", () => {
  // 算错键的表现极具迷惑性：行照样显示「已展开」（那个只看 React 局部 state 的
  // row.expanded），点下去却毫无反应 —— 因为 onCollapse 写的是另一个键。
  // 读键（render 里）必须带上 nodeId，与 handleChildToggle / onCollapse 的三参数
  // 版本一致。buildRows 里那处是第三个读键，同样要带 —— 它决定「哪些子会话被
  // 铺出来」，键算错就等于展开态写进去却读不出来。
  const list = read("src/components/SessionList.tsx");
  assert.match(
    list,
    /const stateKey = childStateKey\(row\.parent, group\.rootId, groupNodeId\);/,
    "render 里的读键少传了 nodeId，会算到另一个键上",
  );
  assert.match(
    list,
    /const stateKey = childStateKey\(item, fallbackRootId, fallbackNodeId\);/,
    "buildRows 里的读键也必须带 nodeId",
  );
  // 写键与 render 读键必须算出同一个键：onCollapse 里的内联写法
  // [childStateKey(row.parent, group.rootId, groupNodeId)]: false
  const writes = list.match(
    /\[childStateKey\(row\.parent, group\.rootId, groupNodeId\)\]: false,/g,
  ) || [];
  assert.equal(writes.length, 1, "onCollapse 的写键应与读键同形");
  // 任何 childStateKey 调用都不得只剩两参（那正是本 bug 的形状）。按实参个数判定，
  // 别靠逗号切串 —— 键里本来就带 `::` 和 `:`。
  const calls = [...list.matchAll(/childStateKey\(([^)]*)\)/g)].map((m) => m[1]);
  assert.ok(calls.length >= 4, "至少应覆盖 buildRows / handleChildToggle / render 三处");
  const twoArg = calls.filter((args) => args.split(",").length !== 3);
  assert.deepEqual(twoArg, [], "不得有非三参数的 childStateKey 调用（少传 nodeId 即本 bug）");
});
