import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const app = fs.readFileSync(path.join(root, "src/App.tsx"), "utf8");
const appTask = fs.readFileSync(path.join(root, "src/app/appTask.ts"), "utf8");
const css = fs.readFileSync(path.join(root, "src/index.css"), "utf8");
const composer = fs.readFileSync(path.join(root, "src/components/composerStyles.tsx"), "utf8");
const promptEditor = fs.readFileSync(path.join(root, "src/components/PromptEditor.tsx"), "utf8");
const tokenEditor = fs.readFileSync(path.join(root, "src/components/editor/TokenEditor.tsx"), "utf8");

// 1) 原生 <select> 统一外观：全局一条规则接管，收起态跟输入框同源。
assert.match(
  css,
  /\nselect \{[\s\S]*?appearance: none;/,
  "global select rule must drop the native appearance",
);
assert.match(
  css,
  /\nselect \{[\s\S]*?background-color: var\(--input-bg/,
  "global select rule must use the app input background (native grey breaks dark mode)",
);
assert.match(
  css,
  /\nselect \{[\s\S]*?background-image: url\("data:image\/svg\+xml/,
  "global select rule must draw its own chevron; the native arrow is invisible on dark",
);
assert.match(css, /select option \{[\s\S]*?var\(--menu-bg/, "option list must follow the menu background");
// 调用点不再各写一份外观，否则内联样式会盖掉全局规则。
assert.doesNotMatch(
  app,
  /<select[\s\S]{0,400}?background: "var\(--input-bg\)"/,
  "the create dialog select must not inline its own background over the shared rule",
);
assert.doesNotMatch(
  app,
  /<select[\s\S]{0,400}?fontSize: "12px"/,
  "the create dialog select must not inline its own font size",
);

// 2) 新建任务时可在编辑区里选 agent / 模型，覆盖下一个 agent 阶段。
//    选择器是 PromptEditor 自带的那一个（不是另画的下拉框），所以要显式打开。
assert.match(
  promptEditor,
  /\{!isUser \|\| showAgentSelector \? \(/,
  "PromptEditor must let a caller force the agent selector on (the create editor is a user stage)",
);
assert.match(
  app,
  /showAgentSelector=\{taskInlineHasAgentStage\}/,
  "the create dialog must turn on the editor's agent selector when the template has an agent stage",
);
for (const prop of ["agents={availableAgents}", "agent={taskInlineAgent}", "model={taskInlineModel}"]) {
  assert.ok(app.includes(prop), `the create editor must receive ${prop}`);
}
// 默认显式选中模板里第一个 agent 段的取值。
assert.match(
  app,
  /const taskInlineAgent = taskInlineEdit\.agentOverride \|\| taskInlineTemplateAgent\?\.agent \|\| "codex";/,
  "the selector must default to the next agent stage's agent",
);
assert.match(
  app,
  /const taskInlineModel = taskInlineEdit\.modelOverride \|\| taskInlineTemplateAgent\?\.model \|\| "";/,
  "the selector must default to the next agent stage's model",
);
assert.match(
  appTask,
  /agentOverride\?: string;/,
  "TaskInlineEditState must carry the agent override",
);
assert.match(
  appTask,
  /modelOverride\?: string;/,
  "TaskInlineEditState must carry the model override",
);
assert.match(
  appTask,
  /effortOverride\?: string;/,
  "TaskInlineEditState must carry the effort override",
);
// 覆盖只作用于「第一个」agent 段，后面的段不动。
assert.match(
  app,
  /if \(snapshot\?\.role !== "agent"\) return snapshot;\s*\n\s*if \(touched\) return snapshot;/,
  "the override must stop after the first agent stage",
);
assert.match(
  app,
  /touched \? stages : undefined/,
  "no agent stage in the template means no override is sent",
);
// 没选覆盖时不能传 stages，让后端照模板走。
assert.match(
  app,
  /const overrideStages = edit\.agentOverride \|\| edit\.modelOverride \|\| edit\.effortOverride/,
  "stages must only be sent when an override was actually chosen",
);
// 不要再在弹窗头部另画下拉框。
assert.doesNotMatch(
  app,
  /aria-label=\{t\("task\.selectAgent"\)\}/,
  "the agent picker must live in the editor, not as a separate header dropdown",
);

// 3) 字号刻度单一来源：token 编辑器与单行输入框都不再写死数字。
assert.match(composer, /export const EDITOR_FONT_SIZE = 16;/, "editor font size must be a named constant");
assert.match(composer, /export const INPUT_FONT_SIZE = 13;/, "input font size must be a named constant");
assert.match(
  tokenEditor,
  /fontSize: `\$\{EDITOR_FONT_SIZE\}px`/,
  "TokenEditor must take its font size from the shared scale",
);
assert.doesNotMatch(
  tokenEditor,
  /fontSize: "16px"/,
  "TokenEditor must not hardcode a font size",
);
assert.match(
  composer,
  /export const composerInputStyle[\s\S]*?fontSize: INPUT_FONT_SIZE/,
  "the shared input style must use the shared scale",
);
// 面板里的单行输入框走共享样式，不再各抄一份 12px。
assert.doesNotMatch(
  fs.readFileSync(path.join(root, "src/components/TaskDetailPanel.tsx"), "utf8"),
  /const inputStyle: React\.CSSProperties/,
  "TaskDetailPanel must not keep a private copy of the compact input style",
);

console.log("task-create-controls.test.mjs: OK");
