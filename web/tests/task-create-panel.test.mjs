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
  /composerEditorInsets\(isMultiLine, \(extraControls \? 132 : 96\) \+ extraControlsExtraInset\)/,
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

// 回车语义：桌面端 Enter 换行、Ctrl/Cmd+Enter 发送；移动端沿用「回车发送」偏好。
// 判定收敛在 shared/sendShortcut 的 resolveEnterIntent，会话输入框与两个任务输入框共用。
const sendShortcut = fs.readFileSync(path.join(root, "src/shared/sendShortcut.ts"), "utf8");
assert.match(promptEditor, /onEnter\?: \(event: KeyboardEvent \| null\) => boolean;/, "PromptEditor must accept an onEnter override");
assert.match(promptEditor, /onEnter=\{handleEnter\}/, "PromptEditor must forward Enter handling to TokenEditor");
assert.match(promptEditor, /resolveEnterIntent\(event, \{/, "PromptEditor must defer Enter semantics to the shared resolver");
assert.match(promptEditor, /if \(intent !== "send"\) return false;/, "a newline intent must fall through to Lexical");
assert.match(promptEditor, /if \(canSend\) flushAndSend\(\);/, "Enter must send when the editor has text and is not sending");
assert.match(promptEditor, /const canSend = !sending && !sendDisabled;/, "send is gated by the sending state and the caller's send gate");
assert.match(sendShortcut, /if \(!options\.isMobile\) return "newline";/, "desktop Enter must insert a newline");
assert.match(sendShortcut, /if \(event\.ctrlKey \|\| event\.metaKey\) return "send";/, "Ctrl/Cmd+Enter must send");

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
// 位置在输入框右下角的控件行，与「选择工作流」并排；详情面板的发送框里没有它。
assert.match(inlinePanel, /active=\{taskInlineEdit\.startImmediately\}/, "the create panel must expose the immediate-run toggle");
assert.match(
  inlinePanel,
  /<RunImmediatelyControl[\s\S]{0,200}?active=\{taskInlineEdit\.startImmediately\}/,
  "the toggle must be the shared run-immediately control",
);
assert.doesNotMatch(inlinePanel, /mode="toggle"|mode="action"/, "the toggle has a single icon-only form");
assert.match(
  inlinePanel,
  /<WorkflowSelector[\s\S]{0,320}?choice=\{taskInlineEdit\.workflowChoice\}/,
  "the create panel must let the first message pick a workflow",
);
assert.match(inlinePanel, /extraControlsExtraInset=\{128\}/, "两个控件都比单颗胶囊宽，正文要让出位置");
assert.doesNotMatch(inlinePanel, /type="checkbox"/, "面板自己不再手搓一个勾选框");
assert.match(tasks, /start_immediately: options\?\.startImmediately \?\? true/, "createTask must send start_immediately and default it to true");
assert.match(tasks, /workflow_choice: options\.workflowChoice/, "createTask must forward the picked workflow_choice");
assert.match(tasks, /workflow_version_id: options\.workflowVersionId/, "createTask must forward the picked workflow version");
assert.match(
  fs.readFileSync(path.join(root, "src/app/useTaskComposer.ts"), "utf8"),
  /workflowChoice: edit\.workflowChoice/,
  "the create dialog must pass its workflow choice into createTask",
);
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

// 详情面板只读对话流；动作全部平铺在 header 右上角（2026-11-22 起不再有菜单）。
// 看板卡片的判据仍住在 taskCardActions；详情口径是 taskDetailActions，两者有意不同。
assert.doesNotMatch(panel, /<StageEditor\b/, "the detail panel no longer renders a stage editor");
assert.doesNotMatch(panelHook, /appendStage|setEditingStage|startEditStage|saveStage|removeTaskStage/, "the detail hook must not keep stage editing state");
const cardActions = fs.readFileSync(path.join(root, "src/components/task/taskCardActions.ts"), "utf8");
assert.match(
  cardActions,
  /if \(task\.status !== "running"\) actions\.push\("complete"\);/,
  "完成 is available to every non-terminal, non-running card",
);
assert.match(
  cardActions,
  /task\.create_worktree === true && !!task\.worktree_path && task\.worktree_missing !== true;/,
  "收尾 must still read the server-derived worktree_missing flag",
);
// 详情 header：动作来自 taskDetailActions 平铺渲染，删除也在同一排。
assert.match(panel, /taskDetailActions\(task\)\.map/, "the panel header renders the flat detail actions");
assert.match(panel, /complete: \(\) => void completeTask\(\)/, "the panel must wire the complete handler");
const headerRightAt = panel.indexOf("headerRight={editingName");
assert.ok(headerRightAt > 0, "TaskDetailPanel must pass a headerRight block");
const headerRightSlice = panel.slice(headerRightAt, headerRightAt + 1600);
assert.match(headerRightSlice, /actions\.map\(\(action\) => \(/, "the header actions are flat buttons");
assert.match(headerRightSlice, /"delete-task": \(\) => void deleteTaskCard\(\)|action\.onSelect/, "delete is one of the flat actions");
assert.doesNotMatch(headerRightSlice, /TaskDetailActionsMenu|aria-haspopup="menu"/, "the actions menu is gone");
assert.doesNotMatch(headerRightSlice, /finishWorktree|completeTask\(\)|reopenTask\(\)/, "actions must not stay as loose hand-written header icons outside the flat map");
assert.doesNotMatch(panel, /rebuildTaskWorktree|TaskRebuildWorktreeIcon|task\.rebuildWorktree/, "the panel must not offer a rebuild-worktree entry");
assert.match(composerIcons, /export function TrashIcon/);
assert.match(panel, /"delete-task": <DeleteIcon \/>/, "删除与其它动作同排渲染");

// 详情面板「完成」的出口：待审阅给完成/取消；完成判据住在 taskDetailActions 纯函数里。
test("the panel's complete gate is non-terminal and not running", () => {
  const gate = (terminal, running) => !terminal && !running;
  assert.equal(gate(false, false), true);
  assert.equal(gate(false, true), false);
  assert.equal(gate(true, false), false);
});

console.log("task-create-panel.test.mjs: OK");
