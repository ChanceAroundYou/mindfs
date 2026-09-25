import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const dialog = fs.readFileSync(path.join(root, "src/components/TaskTemplateDialog.tsx"), "utf8");
const promptEditor = fs.readFileSync(path.join(root, "src/components/PromptEditor.tsx"), "utf8");
const composer = fs.readFileSync(path.join(root, "src/components/composerStyles.tsx"), "utf8");

// 任务模板的输入控件必须复用任务侧那一套，不能各自画一套：
// 曾经这里用裸 <textarea> + 自制 RoleAgentSwitch（内嵌一份配置不同的 AgentSelector），
// 和对话输入框 / 任务面板长得完全不一样，用户看不出它们是同一个东西。

// 1) prompt 模板走 PromptEditor，且 agent 选择器由它自带（不再 hide）。
assert.match(
  dialog,
  /<PromptEditor[\s\S]*?role=\{isAgent \? "agent" : "user"\}/,
  "task template prompts must use the shared PromptEditor",
);
assert.doesNotMatch(
  dialog,
  /hideAgentSelector/,
  "the template dialog must not suppress PromptEditor's agent selector; the shared control owns it",
);
assert.doesNotMatch(
  dialog,
  /<textarea/,
  "task template prompts must not fall back to a raw textarea",
);

// 2) 角色 / 阶段选项不再是这个文件私有的控件：统一到 StageOptionsBar，
//    两处（任务模板编辑 + 任务详情的阶段编辑）共用同一个组件。
assert.doesNotMatch(
  dialog,
  /<AgentSelector/,
  "the template dialog must not embed its own AgentSelector next to PromptEditor's",
);
assert.doesNotMatch(
  dialog,
  /task-template-agent-active/,
  "the accent-background wrapper for the old inline AgentSelector is dead and must be gone",
);
assert.doesNotMatch(
  dialog,
  /AgentIcon/,
  "the role toggle must not draw an agent icon; the selector lives inside the editor",
);
assert.doesNotMatch(
  dialog,
  /function RoleAgentSwitch/,
  "RoleAgentSwitch must be gone; the role toggle lives in the shared StageOptionsBar",
);
assert.doesNotMatch(
  dialog,
  /function StageOptionsMenu/,
  "the 3-dot StageOptionsMenu must be gone; options are now an always-visible bar",
);
assert.match(
  dialog,
  /import \{ StageOptionsBar, type SessionReusePolicy \} from "\.\/StageOptionsBar"/,
  "the template dialog must use the shared StageOptionsBar",
);
assert.match(
  dialog,
  /header=\{\(\s*<StageOptionsBar/,
  "the options must render in PromptEditor's header slot, not hidden in a menu",
);
// PromptEditor 默认按 role 隐藏选择器（user 段没有 agent），但允许调用方显式打开
// ——新建任务面板就是 user 段，却要在里面挑「下一个 agent 阶段」的 agent/模型。
assert.match(
  promptEditor,
  /\{!isUser \|\| showAgentSelector \? \(\s*<AgentSelector/,
  "PromptEditor must hide the agent selector for user stages unless a caller forces it on",
);

// 3) agent/model/mode/effort/fastService 一律经 PromptEditor 的回调落库。
for (const [prop, label] of [
  ["agent={snapshot.agent", "agent"],
  ["model={snapshot.model", "model"],
  ["agentMode={snapshot.mode", "mode"],
  ["effort={snapshot.effort", "effort"],
  ["fastService={snapshot.fast_service", "fast_service"],
]) {
  assert.ok(dialog.includes(prop), `PromptEditor must receive ${label} from the stage snapshot`);
}
for (const handler of [
  "onAgentChange",
  "onModeChange",
  "onEffortChange",
  "onLongContextChange",
  "onFastServiceChange",
]) {
  assert.ok(dialog.includes(handler), `stage ${handler} must route through PromptEditor`);
}

// 4) fastService 是真字段，PromptEditor 不能再写死 noop（那样模板里就再也改不了了）。
assert.match(
  promptEditor,
  /onFastServiceChange=\{editable \? \(onFastServiceChange \|\| noop\) : noop\}/,
  "PromptEditor must forward fastService instead of hardcoding a no-op",
);
assert.doesNotMatch(
  promptEditor,
  /onFastServiceChange=\{noop\}/,
  "PromptEditor must not hardcode onFastServiceChange to noop",
);

// 5) 单行紧凑输入框（模板名 / 阶段名）走共享样式，不再各文件复制一份。
assert.match(
  composer,
  /export const composerInputStyle: React\.CSSProperties/,
  "composerStyles must own the shared compact input style",
);
assert.doesNotMatch(
  dialog,
  /const inputStyle: React\.CSSProperties/,
  "the template dialog must not keep a private copy of the compact input style",
);
assert.ok(
  dialog.includes("composerInputStyle"),
  "the template name and stage name inputs must use the shared compact input style",
);

// 3) StageOptionsBar：两处共用的那一份，且不藏在三点菜单里。
const stageOptions = fs.readFileSync(path.join(root, "src/components/StageOptionsBar.tsx"), "utf8");
const panel = fs.readFileSync(path.join(root, "src/components/TaskDetailPanel.tsx"), "utf8");

for (const key of ["taskTemplate.autoAdvance", "taskTemplate.planMode", "taskTemplate.sessionReuse"]) {
  assert.ok(stageOptions.includes(`t("${key}")`), `the shared bar must expose ${key}`);
}
assert.match(
  stageOptions,
  /aria-pressed=\{!isAgent\}/,
  "the user toggle must expose its state via aria-pressed",
);
assert.match(
  stageOptions,
  /background: active \? "var\(--accent-color\)" : "var\(--input-bg\)"/,
  "the user toggle must highlight when selected",
);
// 会话复用：三个选项都在，且 user 段禁用。
for (const opt of ["task_main", "same_stage", "always_new"]) {
  assert.ok(stageOptions.includes(`value="${opt}"`), `session reuse must offer ${opt}`);
}
assert.match(
  stageOptions,
  /disabled=\{disabled \|\| !isAgent\}/,
  "session reuse is meaningless on a user stage and must be disabled there",
);
// 任务详情里的阶段编辑也用同一份，且排在 header 上。
assert.match(
  panel,
  /import \{ StageOptionsBar, type SessionReusePolicy \} from "\.\/StageOptionsBar"/,
  "TaskDetailPanel must use the shared StageOptionsBar",
);
assert.match(
  panel,
  /header=\{editing \? \(\s*<StageOptionsBar/,
  "the options must sit in the editor header while editing",
);
// 保存时四个选项都要落库（UpdateStage 是全量替换，漏一个就丢一个）。
for (const field of ["auto_advance: editAutoAdvance", "plan_mode: isAgent ? editPlanMode : false", "session_reuse_policy: editSessionReuse", "role: editRole"]) {
  assert.ok(panel.includes(field), `saveStage must persist ${field}`);
}
// 三点菜单那套在两处都得消失。
assert.doesNotMatch(dialog, /StageOptionsMenu/, "template dialog must not keep the 3-dot menu");
assert.doesNotMatch(panel, /StageOptionsMenu/, "task detail must not keep the 3-dot menu");
