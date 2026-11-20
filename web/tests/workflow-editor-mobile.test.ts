import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import {
  DRAFT_PANEL_WIDTH,
  SHEET_MARGIN,
  authoringPanelExpanded,
  boardKeepsWorkflowEditor,
  draftCollapseFrame,
  draftHandleFrame,
  draftPanelFrame,
  inspectorFrame,
} from "../src/components/task/workflowEditorLayout.ts";

const read = (p: string) => readFileSync(new URL(p, import.meta.url), "utf8");

// ── 编辑器只属于看板这一次停留 ────────────────────────────────────────────

test("只有单项目看板保留编辑器标记，工作台/files/chat 一律重置", () => {
  assert.equal(boardKeepsWorkflowEditor("board"), true);
  // 工作台是另一个面（没有编辑器入口）：切过去还留着编辑器，从那儿点开项目就又是编辑器。
  assert.equal(boardKeepsWorkflowEditor("workspace"), false);
  // 看板面板在这两个视图里根本不渲染，open 标记却活在 app 层，必须清掉。
  assert.equal(boardKeepsWorkflowEditor("files"), false);
  assert.equal(boardKeepsWorkflowEditor("chat"), false);
});

test("离开看板就关闭编辑器：hook 按 mainView 重置，而不是等用户手点关闭", () => {
  const hook = read("../src/app/useAppKanbanTaskPanel.tsx");
  assert.match(hook, /boardKeepsWorkflowEditor\(mainView\)/, "判定必须来自 mainView，别用一次性快照");
  assert.match(hook, /if \(!boardVisible\) setWorkflowEditorOpen\(false\)/, "隐藏时清标记，回看板才是看板");
  // mainView 由状态层真实值透传，不能退回 ref（ref 变化不会触发 effect）。
  const composition = read("../src/app/useAppViewComposition.ts");
  assert.match(composition, /useAppKanbanTaskPanel\(\{\s*\n\s*workspaceOpen, mainView,/, "组合层要把 mainView 值传下去");
  const types = read("../src/app/useAppViewCompositionTypes.ts");
  assert.match(types, /mainView: MainViewMode;/, "参数类型要声明 mainView 值");
});

test("返回键先关编辑器（脏改动仍走 confirmLeave），而不是把人退离看板", () => {
  const editor = read("../src/components/task/WorkflowDraftEditor.tsx");
  assert.match(editor, /useBackLayer\(true, close\)/, "编辑器挂载即入返回栈");
  assert.match(editor, /confirmLeave\(\)/, "关闭路径保留脏改动确认");
});

// ── 手机属性面板：贴底抽屉 ────────────────────────────────────────────────

test("桌面卡片贴节点，手机抽屉贴底全宽且不受 position 影响", () => {
  const anchored = inspectorFrame("anchored", { x: 310, y: 200 });
  assert.equal(anchored.left, 310);
  assert.equal(anchored.top, 200);
  assert.equal(anchored.maxWidth, "400px");

  const sheet = inspectorFrame("sheet", { x: 310, y: 200 });
  assert.equal(sheet.left, SHEET_MARGIN);
  assert.equal(sheet.right, SHEET_MARGIN);
  assert.equal(sheet.bottom, SHEET_MARGIN);
  assert.equal(sheet.top, "auto");
  assert.equal(sheet.maxWidth, "none", "390px 的屏放不下 400px 的卡片");
  assert.ok(!("width" in sheet) || sheet.width === undefined);
});

test("窄屏点击节点走抽屉（不做贴节点数学），平移/缩放不再重锚", () => {
  const canvas = read("../src/components/task/WorkflowCanvas.tsx");
  assert.match(canvas, /if \(narrow\) \{\s*\n\s*setInspectorPosition\(\{ x: 0, y: 0 \}\);/, "窄屏直接给抽屉占位");
  assert.match(canvas, /!selectedId \|\| narrow \|\| viewportFrame\.current/, "抽屉模式跳过重锚");
  assert.match(canvas, /layout=\{narrow \? "sheet" : "anchored"\}/, "画布要把抽屉布局传下去");
  const inspector = read("../src/components/task/WorkflowNodeInspector.tsx");
  assert.match(inspector, /inspectorFrame\(layout, position\)/, "定位框只能来自纯函数");
  assert.match(inspector, /data-layout=\{layout\}/);
});

// ── 手机上的编辑能力与排布 ────────────────────────────────────────────────

test("手机可以保存/发布/生成：isMobile 不再出现在禁用条件里", () => {
  const editor = read("../src/components/task/WorkflowDraftEditor.tsx");
  assert.doesNotMatch(editor, /disabled=\{[^}]*isMobile[^}]*\}/, "禁用条件里不许再有 isMobile");
  assert.doesNotMatch(editor, /sendDisabled=\{[^}]*isMobile[^}]*\}/, "生成/创建同样不许按移动端禁用");
  assert.doesNotMatch(editor, /readOnly=\{isMobile\}/);
});

test("手机上草稿列表是浮在画布上的左侧抽屉，可收起且收起后完全隐藏", () => {
  // 用户 2026-11-20：「把工作流列表做成浮动的左侧栏，可以展开收起、竖向排列」，
  // 收起形态选定「完全隐藏 + 悬浮把手」，展开时覆盖画布但不挤压画布宽度。
  const desktop = draftPanelFrame({ isMobile: false, open: true });
  assert.equal(desktop.width, 190, "桌面维持固定竖栏");
  assert.equal(desktop.position, undefined, "桌面在文档流里，不浮动");

  const open = draftPanelFrame({ isMobile: true, open: true });
  assert.equal(open.position, "absolute", "抽屉浮在画布上，不占画布宽度");
  assert.equal(open.left, 0);
  assert.equal(open.top, 0);
  assert.equal(open.bottom, 0);
  assert.equal(open.flexDirection, "column", "竖向排列，不再横着滚");
  assert.equal(open.transform, "translateX(0)");
  assert.equal(open.visibility, "visible");
  assert.equal(open.scrollbarWidth, "none", "抽屉自己的滚动条也不露出来");

  const closed = draftPanelFrame({ isMobile: true, open: false });
  assert.equal(closed.visibility, "hidden", "收起后整块不可见，且点不到");
  assert.match(String(closed.transform), /translateX\(-1/, "靠左移出可视区");

  const editor = read("../src/components/task/WorkflowDraftEditor.tsx");
  assert.match(editor, /data-testid="workflow-draft-panel-toggle"/, "收起后要有左边缘悬浮把手");
  assert.match(editor, /data-testid="workflow-draft-panel-collapse"/, "展开时要有收起按钮");
  assert.match(editor, /draftPanelFrame\(\{ isMobile, open: draftPanelOpen \}\)/, "抽屉框只能来自纯函数");
  assert.match(editor, /if \(isMobile\) setDraftPanelOpen\(false\)/, "选完草稿自动收起，把画布让出来");
  assert.match(editor, /authoringPanelExpanded\(\{/, "展开规则走纯函数");
  // AI 面板本体（含折叠按钮）在 WorkflowAuthoringPanel，编辑器只把展开态传下去。
  const panel = read("../src/components/task/WorkflowAuthoringPanel.tsx");
  assert.match(panel, /data-testid="workflow-authoring-toggle"/);
  assert.match(editor, /position: isMobile \? "relative" : undefined/, "抽屉的定位祖先必须是内容区");

  // 抽屉是 absolute（top/bottom 撑满父列），所以父列必须有高度：没选工作流时
  // 内容只有一行提示，容器塌成十几像素、抽屉会变成一条缝（390×844 实测）。
  assert.match(editor, /minHeight: isMobile \? "min\(60vh, 520px\)" : 0/, "手机内容列要撑出抽屉的高度");
  // 新建完也让开画布，与「选完自动收起」同一个意图。
  const createBlank = editor.slice(editor.indexOf("const createBlank"), editor.indexOf("const cancelCreate"));
  assert.match(createBlank, /if \(isMobile\) setDraftPanelOpen\(false\)/, "新建后同样收起抽屉");

  // 提案/报错/新建流程一定展开，否则「生成完了什么都没发生」。
  const base = { isMobile: true, open: false, createMode: false, hasProposal: false, hasError: false };
  assert.equal(authoringPanelExpanded(base), false);
  assert.equal(authoringPanelExpanded({ ...base, open: true }), true);
  assert.equal(authoringPanelExpanded({ ...base, createMode: true }), true);
  assert.equal(authoringPanelExpanded({ ...base, hasProposal: true }), true);
  assert.equal(authoringPanelExpanded({ ...base, hasError: true }), true);
  // 桌面恒展开，与手机开关无关。
  assert.equal(authoringPanelExpanded({ ...base, isMobile: false }), true);
});

test("手机上藏掉主区右缘的浏览器滚动条（只在编辑器打开期间）", () => {
  assert.match(
    read("../src/components/file/DefaultListView.tsx"),
    /className="mindfs-main-scroll"/,
    "滚动容器要有稳定类名",
  );
  const editor = read("../src/components/task/WorkflowDraftEditor.tsx");
  assert.match(editor, /\.mindfs-main-scroll::-webkit-scrollbar\{display:none/, "Chromium 走伪元素");
  assert.match(editor, /scrollbar-width:none/, "Firefox/标准属性");
  assert.match(editor, /document\.body\.dataset\.workflowEditorMobile = "true"/, "只在编辑器挂载期间生效");
});

test("收起把手与展开把手在同一处竖向中点，关闭键是「×」图标", () => {
  // 用户 2026-11-20：「侧板收起的按钮也放到中间对应位置，不要在顶上。关闭请用“x” icon」。
  const handle = draftHandleFrame();
  const collapse = draftCollapseFrame();
  assert.equal(collapse.top, "50%", "收起键竖向居中，不钉在抽屉标题行");
  assert.equal(collapse.transform, "translateY(-50%)");
  assert.equal(handle.top, collapse.top, "收/放两个动作落在同一高度");
  assert.equal(handle.transform, collapse.transform);
  // 贴在抽屉右缘：left = 抽屉宽度 - 自身宽度；宽度只写一处，两边不许各抄一份。
  assert.equal(collapse.left, `calc(${DRAFT_PANEL_WIDTH} - 28px)`);
  assert.equal(draftPanelFrame({ isMobile: true, open: true }).width, DRAFT_PANEL_WIDTH);
  // 与左缘把手镜像：靠外（右）那侧直角，靠内（左）那侧圆角。
  assert.equal(collapse.borderTopLeftRadius, 10);
  assert.equal(collapse.borderTopRightRadius, undefined);
  assert.ok(
    Number(collapse.zIndex) > Number(draftPanelFrame({ isMobile: true, open: true }).zIndex),
    "收起键要盖在抽屉上，不能被抽屉压住",
  );

  const editor = read("../src/components/task/WorkflowDraftEditor.tsx");
  assert.match(editor, /\.\.\.draftCollapseFrame\(\)/, "收起键的几何只能来自纯函数");
  // 必须是抽屉的**兄弟**节点：抽屉自己 overflowY:auto，放进去的绝对定位按钮会跟列表滚走。
  const collapseAt = editor.indexOf('data-testid="workflow-draft-panel-collapse"');
  const navAt = editor.indexOf('data-testid="workflow-draft-panel"');
  assert.ok(collapseAt > 0 && collapseAt < navAt, "收起键渲染在抽屉之前（兄弟节点，不在标题行里）");

  // 关闭：图标按钮 + 可读名字，不再是一段文字。
  const closeAt = editor.indexOf('data-testid="workflow-editor-close"');
  assert.ok(closeAt > 0, "关闭键有稳定 testid");
  const closeTag = editor.slice(closeAt, editor.indexOf("</button>", closeAt));
  assert.match(closeTag, /aria-label=\{t\("task\.workflowEditor\.close"\)\}/, "图标键必须有无障碍名字");
  assert.match(closeTag, /<CloseIcon/, "关闭键画的是 × 图标");
  assert.doesNotMatch(editor, /\{t\("task\.workflowEditor\.close"\)\}<\/button>/, "文字版关闭键已删");
});

test("顶部描述整行独占，手机上拿到两行完整宽度，不再被挤成单行省略号", () => {
  const editor = read("../src/components/task/WorkflowDraftEditor.tsx");
  // 首行只放「名字 + 编辑/删除 … 关闭」（用户 2026-11-20 的第 8 条），描述单独一行，
  // 所以既不会再被标题按钮挤窄，也不需要再按窄屏切 flexDirection。
  const at = editor.indexOf('data-testid="workflow-draft-description"');
  assert.ok(at > 0, "描述块还在");
  const head = editor.slice(at, at + 600);
  assert.match(head, /whiteSpace: isMobile \? "normal" : "nowrap"/);
  assert.match(head, /WebkitLineClamp: 2/, "最多两行，仍保留省略号兜底");
  // 描述必须在首行收口之后，不能再嵌在标题行里。
  const rowClose = editor.lastIndexOf("<span style={{ flex: 1 }} />", at);
  assert.ok(rowClose > 0 && rowClose < at, "描述是首行之后的一块，不是首行里的列");
});

test("手机画布有固定高度、加节点按钮只留图标、提示改成触屏手势", () => {
  const canvas = read("../src/components/task/WorkflowCanvas.tsx");
  assert.match(canvas, /height: narrow \? "min\(58vh, 520px\)" : undefined/, "窄屏父列是自然高度，flex 会解析成 0");
  assert.match(canvas, /\{narrow \? null : labels\[k\]\}/, "窄屏按钮只留图标");
  assert.match(canvas, /aria-label=\{labels\[k\]\}/, "只留图标也必须有可读名字");
  assert.match(canvas, /narrow \? t\("task\.workflowCanvas\.touchHint"\)/, "手机没有 Esc/Delete 键");
  // 旧文案「移动端仅查看」不能再出现（mobileHint 与 mobileViewOnly 两处都已删）。
  const zh = read("../src/i18n/locales/zh-CN-tasks.ts");
  const en = read("../src/i18n/locales/en-US-tasks.ts");
  for (const locale of [zh, en]) {
    assert.ok(!locale.includes("mobileHint"), "移动端只读的文案已经过时");
    assert.ok(!locale.includes("mobileViewOnly"), "移动端只读模式的文案已经过时");
    assert.match(locale, /"task\.workflowCanvas\.touchHint"/);
    assert.match(locale, /"task\.workflowCanvas\.readOnlyHint"/);
  }
});
