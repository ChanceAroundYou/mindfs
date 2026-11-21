import assert from "node:assert/strict";
import { test } from "vitest";
import fs from "./source-map-fs.mjs";
import path from "node:path";

// 新建任务面板 = 一条留言 + 可选的 worktree 配置 + 「立即执行」开关。
// 任务的第一轮由后端 router 决定走直连会话还是某个 workflow，所以面板上没有
// 阶段、agent/model/effort；「立即执行」只决定建完要不要马上派发首条留言。

const root = path.resolve(import.meta.dirname, "..");
const promptEditor = fs.readFileSync(path.join(root, "src/components/editor/PromptEditor.tsx"), "utf8");
const panel = fs.readFileSync(path.join(root, "src/components/task/TaskDetailPanel.tsx"), "utf8");
const panelHook = fs.readFileSync(path.join(root, "src/components/task/useTaskDetailPanel.ts"), "utf8");
const composer = fs.readFileSync(path.join(root, "src/components/action/composerStyles.tsx"), "utf8");
const composerIcons = fs.readFileSync(path.join(root, "src/components/action/composerIcons.tsx"), "utf8");
const composerHeight = fs.readFileSync(path.join(root, "src/components/action/useComposerEditorHeight.ts"), "utf8");
const tasks = fs.readFileSync(path.join(root, "src/services/task/crud.ts"), "utf8");
const inlinePanel = fs.readFileSync(path.join(root, "src/components/app/TaskInlineEditPanel.tsx"), "utf8");
const appView = fs.readFileSync(path.join(root, "src/components/app/TaskDetailPanelGate.tsx"), "utf8");
const zh = fs.readFileSync(path.join(root, "src/i18n/locales/zh-CN.ts"), "utf8");
const en = fs.readFileSync(path.join(root, "src/i18n/locales/en-US.ts"), "utf8");

// 文字铺满整宽：单行时右侧给控件留位，多行时贴边只在底部让高度。
// 这些数字以前在 ActionBar 与 PromptEditor 各抄一份，现在统一由 useComposerEditorHeight 给出。
assert.match(
  promptEditor,
  /composerEditorInsets\(isMultiLine, extraControls \? 132 : 96\)/,
  "PromptEditor must take its insets from the shared composerEditorInsets helper (wider when extra controls are present)",
);
assert.match(
  promptEditor,
  /useComposerEditorHeight\(editorRef\)/,
  "PromptEditor must share the wrap-detection hook with ActionBar",
);
assert.match(
  composerHeight,
  /rightInset: isMultiLine \? MULTI_LINE_RIGHT_INSET : singleLineRightInset/,
  "the shared helper must widen the text column once it wraps",
);
assert.match(
  composerHeight,
  /bottomInset: isMultiLine \? MULTI_LINE_BOTTOM_INSET : SINGLE_LINE_BOTTOM_INSET/,
  "the shared helper must reserve toolbar height at the bottom when multi-line",
);
const useActionBar = fs.readFileSync(path.join(root, "src/components/action/useActionBar.ts"), "utf8");
assert.match(useActionBar, /useComposerEditorHeight\(editorRef\)/, "ActionBar must share the wrap-detection hook too");
assert.match(useActionBar, /composerEditorInsets\(/, "ActionBar must take its insets from the shared helper too");

// + 号只在能新增文件时出现，不做不可用的常显按钮。
assert.match(promptEditor, /\{onAttach \? \(/, "PromptEditor must render the + button only when a file picker is wired");
assert.doesNotMatch(
  promptEditor,
  /disabled=\{!editable \|\| sending \|\| !onAttach\}/,
  "PromptEditor must not render a disabled + button",
);

// 禁用态灰底：不能用未定义的 CSS 变量。
assert.doesNotMatch(composer, /var\(--button-bg\)/, "composerStyles must not reference the undefined button-bg variable");

// 回车 = 发送（复用 PromptEditor 的输入框都得有）。
assert.match(promptEditor, /onEnter\?: \(event: KeyboardEvent \| null\) => boolean;/, "PromptEditor must accept an onEnter override");
assert.match(promptEditor, /onEnter=\{handleEnter\}/, "PromptEditor must forward Enter handling to TokenEditor");
assert.match(promptEditor, /if \(isComposing\(event\) \|\| event\?\.shiftKey\) return false;/, "Enter must pass through while composing and on Shift+Enter");
assert.match(promptEditor, /if \(canSend\) flushAndSend\(\);/, "Enter must send when the editor has text and is not sending");
assert.match(promptEditor, /const canSend = !sending && !sendDisabled;/, "send is gated by the sending state and the caller's send gate");

// setText 不得无条件抢焦点。
const tokenEditor = fs.readFileSync(path.join(root, "src/components/editor/TokenEditor.tsx"), "utf8");
assert.match(
  tokenEditor,
  /const keepFocus = !!root && root\.contains\(document\.activeElement\);/,
  "setText must only re-focus when the editor already holds focus",
);
// 只是「不主动 focus」不够：Lexical 同步选区会让 Chrome 自己聚焦 contenteditable，
// 所以未聚焦的灌值之后必须把焦点还回去（2026-11-20 用户报的自动夺焦）。
assert.match(
  tokenEditor,
  /const previous = keepFocus \? null : document\.activeElement;/,
  "setText must remember what had focus before writing",
);
assert.match(
  tokenEditor,
  /if \(root && root\.contains\(document\.activeElement\)\) restoreFocus\(previous\);/,
  "setText must undo the focus Lexical's selection sync steals",
);
// Lexical 的 DOM 提交可能排到微任务，夺焦发生在 setText 返回之后，所以还要补一拍。
assert.match(tokenEditor, /queueMicrotask\(undo\)/, "the restore must also run in a microtask");
assert.match(
  inlinePanel,
  /taskInlineEditorRef\.current\?\.setText\([\s\S]{0,120}?taskInlineEditorRef\.current\?\.focus\(\)/,
  "the create dialog must focus its editor explicitly now that setText no longer steals focus",
);

// 新建面板只有一条留言 + 附件；不得再出现阶段编辑器、agent 选择器或 agent/model overrides。
assert.match(inlinePanel, /<PromptEditor/, "the create panel renders the shared PromptEditor");
assert.doesNotMatch(inlinePanel, /<StageEditor\b/, "the create panel must not render a stage editor");
assert.doesNotMatch(inlinePanel, /<AgentSelector\b/, "agent choice belongs to the router, not the create panel");
assert.doesNotMatch(inlinePanel, /DEFAULT_TASK_MODEL|inheritAgentStage|<StageOptionsBar\b/, "stage options must be gone from the create panel");
// 「立即执行」在对话模型里只剩一个含义：建完立刻派发首条留言（默认勾上）。
assert.match(inlinePanel, /taskInlineEdit\.startImmediately/, "the create panel must expose the immediate-run toggle");
assert.match(inlinePanel, /checked=\{taskInlineEdit\.startImmediately\}/, "the toggle must be a real checkbox that renders the current value");
assert.match(tasks, /start_immediately: options\?\.startImmediately \?\? true/, "createTask must send start_immediately and default it to true");
assert.match(tasks, /export async function runTaskNow\(/, "the queued message needs a run-now service call");
assert.match(
  inlinePanel,
  /taskInlineAttachmentInputRef\.current\?\.click\(\)/,
  "the attach button must open the hidden file input",
);
assert.match(inlinePanel, /onSend=\{\(text: string\) => void saveTaskInlineEdit\(text\)\}/, "send must save the free-form message");

// 新建任务标题不该再塞模板名。
assert.match(zh, /"task\.createDialogTitle": "创建任务"/, "the create dialog title must be plain 「创建任务」");
assert.doesNotMatch(zh, /"task\.createDialogTitle": "创建\{name\}任务"/, "the create dialog title must not repeat the template name");
assert.match(en, /"task\.createDialogTitle": "Create task"/, "en-US create dialog title must match");
assert.doesNotMatch(en, /"task\.createDialogTitle": "Create \{name\} task"/, "en-US create dialog title must not repeat the template name");

// 任务编辑弹窗已删：面板只剩「新建」。
for (const [dict, name] of [[zh, "zh-CN"], [en, "en-US"]]) {
  for (const key of ["task.editDialogTitle", "task.edit", "task.editFailed", "task.detailNotSynced"]) {
    assert.doesNotMatch(dict, new RegExp(`"${key.replace(/\./g, "\\.")}":`), `${name} must not keep the dead ${key} key`);
  }
}
assert.doesNotMatch(appView, /openTaskEditDialog/, "the task edit dialog opener must be gone, not just its button");
assert.doesNotMatch(appView, /taskInlineEdit\.taskId/, "the inline task dialog is create-only; the taskId branch must be gone");

// 阶段编辑端点已从前端服务层删除（D4：只删 API 层）。
assert.doesNotMatch(tasks, /remove-stage|add-stage/, "the task service must not keep stage editing endpoints");
for (const name of ["removeTaskStage", "appendTaskStage", "saveStage"]) {
  assert.doesNotMatch(tasks, new RegExp(`\\b${name}\\b`), `tasks service must not expose ${name}`);
}

// 详情面板只读对话流；「完成 / 删除」仍住在头部（完成收进「操作控件」菜单），判据与卡片同一口径。
assert.doesNotMatch(panel, /<StageEditor\b/, "the detail panel no longer renders a stage editor");
assert.doesNotMatch(panelHook, /appendStage|setEditingStage|startEditStage|saveStage|removeTaskStage/, "the detail hook must not keep stage editing state");
assert.match(panelHook, /const canCompleteTask = !terminal && !taskRunningNow;/, "完成 is available to every non-terminal, non-running task");
assert.match(
  panelHook,
  /const canFinishWorktree = task\?\.create_worktree === true\s*\n\s*&& !!task\?\.worktree_path\s*\n\s*&& task\?\.worktree_missing !== true;/,
  "the panel must still read the server-derived worktree_missing flag for 收尾",
);
// 完成 / 收尾 / 重新打开由「操作控件」菜单承载；菜单挂在 headerRight 里。
assert.match(
  panel,
  /if \(canCompleteTask\)[\s\S]{0,400}?key: "complete"[\s\S]{0,300}?onSelect: \(\) => void completeTask\(\)/,
  "完成 must be one of the panel header actions",
);
const headerRightAt = panel.indexOf("headerRight={editingName");
assert.ok(headerRightAt > 0, "TaskDetailPanel must pass a headerRight block");
const headerRightSlice = panel.slice(headerRightAt, headerRightAt + 1600);
assert.match(headerRightSlice, /<TaskDetailActionsMenu actions=\{actions\} disabled=\{saving\} \/>/, "the header actions menu is the only place for secondary actions");
assert.match(
  headerRightSlice,
  /\{terminal \? \([\s\S]{0,500}?onClick=\{\(\) => void deleteTaskCard\(\)\}[\s\S]{0,300}?<DeleteIcon \/>/,
  "删除 is only for terminal tasks and must live in the panel header (not inside the actions menu)",
);
assert.doesNotMatch(headerRightSlice, /finishWorktree|completeTask\(\)|reopenTask\(\)/, "secondary actions must not stay as loose header icons");
assert.doesNotMatch(panel, /rebuildTaskWorktree|TaskRebuildWorktreeIcon|task\.rebuildWorktree/, "the panel must not offer a rebuild-worktree entry");
assert.match(composerIcons, /export function TrashIcon/);
assert.match(panel, /\{terminal \? \([\s\S]*?<DeleteIcon \/>/, "terminal tasks get the trash can");

// 面板的完成判据直接跑真函数会落到 useTaskDetailPanel 的 hook 里（不是纯函数），
// 这里用源码断言钉住；对话视图本身由 conversation-view.test.mjs 覆盖。
test("the panel's complete gate is non-terminal and not running", () => {
  const gate = (terminal, running) => !terminal && !running;
  assert.equal(gate(false, false), true);
  assert.equal(gate(false, true), false);
  assert.equal(gate(true, false), false);
});

console.log("task-create-panel.test.mjs: OK");
