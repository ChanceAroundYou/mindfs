import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

// 两张面板深度统一：任务详情 / 新建任务都只有一条对话流 + 一个输入框，
// 共用 PanelShell。阶段编辑器、agent 覆盖、阶段选项条都已随会话模型删除。
// 字段说明与交互细节见 task-panel-options；状态色见 task-card-status。

test("task-panel-unification", async () => {
const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

const panel = read("src/components/task/TaskDetailPanel.tsx");
const inlinePanel = read("src/components/app/TaskInlineEditPanel.tsx");
const panelShell = read("src/components/shell/PanelShell.tsx");
const promptEditor = read("src/components/editor/PromptEditor.tsx");
const conversation = read("src/components/task/ConversationView.tsx");

// 1) 面板各自渲染自己的内容：历史任务与新建任务都走对话式视图。
assert.doesNotMatch(panel, /import \{ StageEditor \} from "\.\/StageEditor"/, "the detail panel no longer edits stages inline");
assert.doesNotMatch(panel, /LegacyTaskView/, "the legacy read-only view was deleted with the stage model");
assert.match(panel, /<ConversationView/, "every task renders through ConversationView");
assert.match(conversation, /CommentThread|comments|活动|activities/i, "ConversationView is the single conversation stream");
assert.match(panel, /import \{ PanelShell,/, "task detail must use PanelShell");
assert.match(inlinePanel, /import \{ PanelShell \} from "\.\.\/shell\/PanelShell"/, "create-task panel must use PanelShell");

// 2) 弹窗外壳不再各手搓遮罩 / 阴影 / buttonStyle
for (const [name, src] of [["TaskDetailPanel", panel], ["TaskInlineEditPanel", inlinePanel]]) {
  assert.doesNotMatch(src, /rgba\(15, 23, 42, 0\.36\)/, `${name} must not hand-roll its own overlay; PanelShell owns it`);
}
assert.doesNotMatch(panel, /function buttonStyle/, "the duplicate buttonStyle in TaskDetailPanel must be gone");
assert.match(panelShell, /export function panelButtonStyle/, "PanelShell owns the shared button style");
assert.match(panelShell, /zIndex: 90/, "PanelShell must pin one z-index for all panels");

// 3) 新建面板 = PromptEditor + 附件。没有阶段编辑器 / agent 选择器 / 角色开关。
assert.match(inlinePanel, /<PromptEditor/, "the create-task panel renders the shared PromptEditor");
assert.doesNotMatch(inlinePanel, /<StageEditor\b/, "the create-task panel must not render a StageEditor");
assert.doesNotMatch(inlinePanel, /<StageOptionsBar\b/, "stage options are gone");
assert.doesNotMatch(inlinePanel, /<AgentSelector\b/, "agent choice belongs to the router");
for (const key of ["agentOverride", "modelOverride", "effortOverride", "start_immediately", "defaultTaskFlowStages"]) {
  assert.doesNotMatch(inlinePanel, new RegExp(key), `the create panel must not carry ${key}`);
}
// PromptEditor 本身也不再接 agent/角色/铅笔。
assert.doesNotMatch(promptEditor, /AgentSelector|onToggleRole|showAgentSelector|isUser/, "PromptEditor is a free-form message box now");
assert.doesNotMatch(promptEditor, /header=/, "PromptEditor has no stage-options header slot");

// 附件「+」：面板底部的隐藏 file input 和缩略图 chips 一直都在，
// 只是没告诉编辑器有这个按钮可点（PromptEditor 的 + 只在传 onAttach 时渲染）。
assert.match(
  inlinePanel,
  /onAttach=\{\(\) => taskInlineAttachmentInputRef\.current\?\.click\(\)\}/,
  "the create-task panel must pass onAttach so PromptEditor renders the add-attachment button",
);
assert.match(inlinePanel, /ref=\{taskInlineAttachmentInputRef\}\s*type="file"/, "the hidden file input the + button clicks must exist in the create-task panel");

// 4) 建任务只发一条消息 + 可选 worktree，不再解析阶段/覆盖。
const taskComposer = read("src/app/useTaskComposer.ts");
assert.doesNotMatch(taskComposer, /taskStagesForCreate|defaultTaskFlowStages|effortOverride/, "create-task must not build a stage list");
// 「立即执行」是建完是否马上派发首条留言的开关（默认 true），不是阶段推进。
assert.match(taskComposer, /startImmediately: true/, "the create dialog must default the immediate-run toggle to on");
assert.match(taskComposer, /startImmediately: edit\.startImmediately/, "the dialog's choice must reach the create payload");
assert.match(taskComposer, /createTask\(/, "create-task still goes through the shared service");

console.log("task-panel-unification.test.mjs: OK");
});
