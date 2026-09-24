import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const promptEditor = fs.readFileSync(path.join(root, "src/components/PromptEditor.tsx"), "utf8");
const panel = fs.readFileSync(path.join(root, "src/components/TaskDetailPanel.tsx"), "utf8");
const stageEditor = fs.readFileSync(path.join(root, "src/components/StageEditor.tsx"), "utf8");
const composer = fs.readFileSync(path.join(root, "src/components/action/composerStyles.tsx"), "utf8");
const composerHeight = fs.readFileSync(path.join(root, "src/components/action/useComposerEditorHeight.ts"), "utf8");
const tasks = fs.readFileSync(path.join(root, "src/services/tasks.ts"), "utf8");
const app = fs.readFileSync(path.join(root, "src/App.tsx"), "utf8");
const zh = fs.readFileSync(path.join(root, "src/i18n/locales/zh-CN.ts"), "utf8");
const en = fs.readFileSync(path.join(root, "src/i18n/locales/en-US.ts"), "utf8");

// 文字铺满整宽：单行时右侧给控件留位，多行时贴边只在底部让高度。
// 这些数字以前在 ActionBar 与 PromptEditor 各抄一份，现在统一由 useComposerEditorHeight 给出——
// 两边共用同一 hook 与同一组 inset，改一处不会只改一半。
assert.match(
  promptEditor,
  /composerEditorInsets\(isMultiLine, 96\)/,
  "PromptEditor must take its insets from the shared composerEditorInsets helper",
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
// ActionBar 也必须走同一个 hook，否则"共用"只是嘴上说说
const actionBar = fs.readFileSync(path.join(root, "src/components/ActionBar.tsx"), "utf8");
assert.match(actionBar, /useComposerEditorHeight\(editorRef\)/, "ActionBar must share the wrap-detection hook too");
assert.match(actionBar, /composerEditorInsets\(/, "ActionBar must take its insets from the shared helper too");

// + 号只在能新增文件时出现，不做不可用的常显按钮。
assert.match(promptEditor, /\{onAttach \? \(/, "PromptEditor must render the + button only when a file picker is wired");
assert.doesNotMatch(
  promptEditor,
  /disabled=\{!editable \|\| sending \|\| !onAttach\}/,
  "PromptEditor must not render a disabled + button",
);

// 禁用态灰底：不能用未定义的 CSS 变量（button-bg 全仓库无定义 → 实际透明，看不出「不可按」）。
assert.doesNotMatch(
  composer,
  /var\(--button-bg\)/,
  "composerStyles must not reference the undefined button-bg variable",
);

// 非编辑态必须显示该阶段自身的 agent/model —— 曾经一律传打开面板时的草稿，
// 结果所有阶段卡（含已执行的 claude/sonnet 卡）都显示 codex 无模型。
// 兜底默认是 claude（DEFAULT_TASK_AGENT），不是 codex。
assert.match(
  stageEditor,
  /agent=\{stage\.agent \|\| DEFAULT_TASK_AGENT\}/,
  "StageEditor must show each stage's own agent (not a panel-wide default)",
);
assert.doesNotMatch(
  stageEditor,
  /agent=\{stage\.agent \|\| "codex"\}/,
  "StageEditor must not fall back to codex when a stage has no agent",
);
assert.match(
  stageEditor,
  /model=\{stage\.model \|\| ""\}/,
  "StageEditor must show each stage's own model when not editing",
);
assert.match(
  stageEditor,
  /effort=\{stage\.effort \|\| ""\}/,
  "StageEditor must show each stage's own effort when not editing",
);

// 未执行阶段可删除；已执行的不给删除入口。
assert.match(panel, /import \{[^}]*TrashIcon[^}]*\} from "\.\/action\/composerStyles"/, "delete icon must come from the shared composerStyles set");
assert.match(panel, /\{!executed && !isCurrent \? \(/, "TaskDetailPanel must offer delete only on unexecuted, non-current stages");
assert.match(panel, /removeTaskStage\(task\.root_id, task\.id, index, nodeId\)/, "TaskDetailPanel must call removeTaskStage");
assert.match(panel, /requestRemoveStage\(index\)/, "delete button must go through the confirm flow");
assert.match(tasks, /\/api\/tasks\/\$\{encodeURIComponent\(taskId\)\}\/remove-stage/, "tasks.ts must POST to the remove-stage endpoint");

// 回车 = 发送（复用 PromptEditor 的输入框都得有，否则只能点按钮）。
// 曾经 PromptEditor 不接 onEnter，TokenEditor 插入换行 —— 面板里回车永远发不出去。
assert.match(
  promptEditor,
  /onEnter\?: \(event: KeyboardEvent \| null\) => boolean;/,
  "PromptEditor must accept an onEnter override",
);
assert.match(
  promptEditor,
  /onEnter=\{handleEnter\}/,
  "PromptEditor must forward Enter handling to TokenEditor",
);
assert.match(
  promptEditor,
  /if \(isComposing\(event\) \|\| event\?\.shiftKey\) return false;/,
  "Enter must pass through while composing (IME) and on Shift+Enter",
);
assert.match(
  promptEditor,
  /if \(canSend\) onSend\?\.\(\);/,
  "Enter must send when the editor is editable and has text",
);

// setText 不得无条件抢焦点：常驻挂载的编辑器（如工作台快速发起）一挂载就把整页焦点夺走。
const tokenEditor = fs.readFileSync(path.join(root, "src/components/editor/TokenEditor.tsx"), "utf8");
assert.match(
  tokenEditor,
  /const keepFocus = !!rootRef\.current && rootRef\.current\.contains\(document\.activeElement\);/,
  "setText must only re-focus when the editor already holds focus",
);
assert.match(
  app,
  /taskInlineEditorRef\.current\?\.setText\([\s\S]{0,120}?taskInlineEditorRef\.current\?\.focus\(\)/,
  "the create dialog must focus its editor explicitly now that setText no longer steals focus",
);

// 新建任务标题不该再塞模板名：模板名已经由标题右侧的下拉框显示，重复一遍看着像两处设置。
assert.match(
  zh,
  /"task\.createDialogTitle": "创建任务"/,
  "the create dialog title must be plain 「创建任务」",
);
assert.doesNotMatch(
  zh,
  /"task\.createDialogTitle": "创建\{name\}任务"/,
  "the create dialog title must not repeat the template name",
);
assert.match(
  en,
  /"task\.createDialogTitle": "Create task"/,
  "en-US create dialog title must match",
);
assert.doesNotMatch(
  en,
  /"task\.createDialogTitle": "Create \{name\} task"/,
  "en-US create dialog title must not repeat the template name",
);
// 任务小卡片的编辑按钮和它开的编辑态弹窗都删了：面板只剩「新建」，
// 所以 editDialogTitle / edit / editFailed / detailNotSynced 全部没有引用了。
for (const [dict, name] of [[zh, "zh-CN"], [en, "en-US"]]) {
  for (const key of ["task.editDialogTitle", "task.edit", "task.editFailed", "task.detailNotSynced"]) {
    assert.doesNotMatch(
      dict,
      new RegExp(`"${key.replace(/\./g, "\\.")}":`),
      `${name} must not keep the dead ${key} key after the task edit dialog was removed`,
    );
  }
}
assert.doesNotMatch(
  app,
  /openTaskEditDialog/,
  "the task edit dialog opener must be gone, not just its button",
);
assert.doesNotMatch(
  app,
  /taskInlineEdit\.taskId/,
  "the inline task dialog is create-only; the taskId branch must be gone",
);

// 「任务输入」三处说法统一成同一个词。
assert.match(zh, /"task\.initialInput": "任务输入"/, "zh-CN must label the first stage 任务输入");
assert.doesNotMatch(zh, /"task\.initialInput": "任务初始输入"/, "zh-CN must drop the 任务初始输入 wording");
assert.match(en, /"task\.initialInput": "Task input"/, "en-US must label the first stage Task input");

// 新增阶段：复制上一段的 agent/model，模板面板和任务详情都走同一个函数。
const appTask = fs.readFileSync(path.join(root, "src/app/appTask.ts"), "utf8");
const templateDialog = fs.readFileSync(path.join(root, "src/components/TaskTemplateDialog.tsx"), "utf8");
assert.match(
  appTask,
  /export const DEFAULT_TASK_AGENT = "claude"/,
  "new agent stages must default to claude, not codex",
);
assert.match(
  appTask,
  /export const DEFAULT_TASK_MODEL = "sonnet"/,
  "new agent stages must default to sonnet",
);
assert.match(
  appTask,
  /export function inheritAgentStage\(stages: StageTemplate\[\], fromIndex: number\)/,
  "inheritAgentStage copies the closest previous agent stage",
);
assert.match(
  templateDialog,
  /snapshot: \{ \.\.\.blankAgentStage\(prev\.stages\[prev\.stages\.length - 1\]\?\.snapshot\)/,
  "template addStage must inherit the last stage's agent/model",
);
assert.match(
  panel,
  /const stage = inheritAgentStage\(stages, stages\.length\);/,
  "task detail addStage must inherit the last stage's agent/model",
);
// 新增完直接落在编辑态，省得再点一次铅笔。
assert.match(
  panel,
  /setEditingStage\(\(next\.task\.stages\?\.length \|\| 1\) - 1\);/,
  "a freshly added stage must open in edit mode",
);
// 两张面板都不许再留 codex 兜底。
for (const [src, name] of [[panel, "TaskDetailPanel"], [templateDialog, "TaskTemplateDialog"], [app, "App"]]) {
  assert.doesNotMatch(
    src,
    /editAgent[^\n]*"codex"|agent: "codex"|\|\| "codex"/,
    `${name} must not fall back to codex`,
  );
}

console.log("task-stage-panel.test.mjs: OK");
