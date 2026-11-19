import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import {
  SHEET_MARGIN,
  authoringPanelExpanded,
  boardKeepsWorkflowEditor,
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

test("手机上草稿列表变成横向一条，AI 面板可收起且出结果必展开", () => {
  const editor = read("../src/components/task/WorkflowDraftEditor.tsx");
  assert.match(editor, /flexDirection: isMobile \? "column" : "row"/, "窄屏改成上下排");
  assert.match(editor, /flexDirection: isMobile \? "row" : "column"/, "草稿列表改成横向条");
  assert.match(editor, /width: isMobile \? "100%" : 190/, "190px 竖栏在手机上会吃掉半屏");
  assert.match(editor, /authoringPanelExpanded\(\{/, "展开规则走纯函数");
  assert.match(editor, /data-testid="workflow-authoring-toggle"/);

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
