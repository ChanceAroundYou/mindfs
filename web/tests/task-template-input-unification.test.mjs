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

// 2) RoleAgentSwitch 只剩 user/agent 角色切换，不再自己内嵌 AgentSelector。
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
assert.match(
  dialog,
  /function RoleAgentSwitch\(\{\s*role,\s*disabled,\s*agent,\s*onUserClick,\s*onAgentActivate,?\s*\}:\s*\{[^}]*role: "user" \| "agent";[^}]*agent: string;[^}]*onUserClick: \(\) => void;[^}]*onAgentActivate: \(\) => void;[^}]*\}\)/,
  "RoleAgentSwitch must shrink to a pure role toggle (role/agent + the two click handlers)",
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
