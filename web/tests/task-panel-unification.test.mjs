import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";
import { readGoPackage } from "./go-source.mjs";

// 两张面板深度统一：任务详情 / 新建任务都编辑同一种东西（阶段定义 + prompt），
// 共用 StageEditor + PanelShell，只剩语义差异。任务模板面板已随模板功能下线（Phase 3）。
// 字段说明与交互细节见 task-panel-options；已执行阶段只读、状态不绑会话、
// SW 缓存与状态色见 task-card-status。

test("task-panel-unification", async () => {
const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

const panel = read("src/components/task/TaskDetailPanel.tsx");
// 新建任务面板（TaskInlineEditPanel）与 AppRootView 都不在虚拟 "src/App.tsx" 里，直接读真文件。
const inlinePanel = read("src/components/app/TaskInlineEditPanel.tsx");
const panelShell = read("src/components/shell/PanelShell.tsx");
const stageEditor = read("src/components/task/StageEditor.tsx");
const stageOptions = read("src/components/task/StageOptionsBar.tsx");
const promptEditor = read("src/components/editor/PromptEditor.tsx");
const appTask = read("src/app/appTask.ts");
const taskComposer = read("src/app/useTaskComposer.ts");
// Go 侧按**包**读，不钉文件名：2026-10-16 的 B1–B10 拆分把 service.go /
// service_tasks.go 切成职责文件（service_run_moves.go、service_create.go…），
// 实现没少、只是搬了家。
const serviceGo = readGoPackage("server/internal/kanban");

// 1) 面板各自渲染自己的内容：历史任务与新建任务都走对话式视图；
//    阶段编辑只留在新建任务面板（详情面板不再内联阶段循环或兼容视图）。
assert.doesNotMatch(panel, /import \{ StageEditor \} from "\.\/StageEditor"/, "the detail panel no longer edits stages inline");
assert.doesNotMatch(panel, /LegacyTaskView/, "the legacy read-only view was deleted with the stage model");
assert.match(panel, /<ConversationView/, "every task renders through ConversationView");
// 新建任务面板由 AppRootView 渲染（App.tsx 只剩四层聚合调用）。
assert.match(inlinePanel, /import \{ StageEditor \} from "\.\.\/task\/StageEditor"/, "create-task panel must use StageEditor");
assert.match(panel, /import \{ PanelShell,/, "task detail must use PanelShell");
assert.match(inlinePanel, /import \{ PanelShell \} from "\.\.\/shell\/PanelShell"/, "create-task panel must use PanelShell");

// 2) 弹窗外壳不再各手搓遮罩 / 阴影 / buttonStyle
for (const [name, src] of [["TaskDetailPanel", panel], ["TaskInlineEditPanel", inlinePanel]]) {
  assert.doesNotMatch(
    src,
    /rgba\(15, 23, 42, 0\.36\)/,
    `${name} must not hand-roll its own overlay; PanelShell owns it`,
  );
}

assert.doesNotMatch(panel, /function buttonStyle/, "the duplicate buttonStyle in TaskDetailPanel must be gone");
assert.match(panelShell, /export function panelButtonStyle/, "PanelShell owns the shared button style");
assert.match(panelShell, /zIndex: 90/, "PanelShell must pin one z-index for all panels");

// 3) StageEditor 固定组合「段名 + StageOptionsBar + PromptEditor」
assert.match(stageEditor, /<StageOptionsBar/, "StageEditor must render the shared options bar");
assert.match(stageEditor, /<PromptEditor/, "StageEditor must render the shared PromptEditor");
assert.match(stageEditor, /header=\{\(/, "the options bar goes in PromptEditor's header slot");
// 段名可选：新建任务面板那一段没有名字概念
assert.match(stageEditor, /stageNamePlaceholder \? \(/, "the stage name input is opt-in via stageNamePlaceholder");
assert.doesNotMatch(
  inlinePanel.slice(inlinePanel.indexOf("<StageEditor")),
  /stageNamePlaceholder=/,
  "the create-task dialog must not render a stage name (that stage is always 任务输入)",
);
// user 段也能显示 agent 选择器（新建任务面板要选下一段的 agent/模型）
assert.match(promptEditor, /\{!isUser \|\| showAgentSelector \? \(/, "PromptEditor must allow forcing the selector on for user stages");
assert.match(inlinePanel, /showAgentSelector=\{taskInlineHasAgentStage\}/, "the create-task editor must show the agent selector");

// 4) 角色切换：user 开关贴在段名旁；不给 onToggleRole 只是「这一段角色锁死」，
//    不会连带把自动推进/计划/会话复用也一起禁掉 —— 曾经用 readOnly={!onToggleRole}
//    表达「角色不可切」，结果详情面板未进编辑态时四个选项全部变灰。
assert.match(
  stageEditor,
  /disabled=\{!onToggleRole\}[\s\S]*?style=\{roleToggleStyle\(!isAgent, !onToggleRole\)\}/,
  "the user toggle locks on its own, independent of the other options",
);
assert.match(
  stageEditor,
  /readOnly=\{mode !== "editable"\}/,
  "the options bar follows the editor's editable state, not the role-toggle handler",
);
assert.doesNotMatch(
  stageEditor,
  /readOnly=\{!onToggleRole\}/,
  "a locked role must not disable the other three options",
);
assert.doesNotMatch(panel, /onToggleRole=\{editing \?/, "the detail panel no longer edits stages inline");
// 会话复用：不再有「会话复用」前缀文字，收起态直接显示当前策略
assert.doesNotMatch(stageOptions, /\{t\("task\.stage\.sessionReuse"\)\}<\/span>/, "the session-reuse label must be gone; the dropdown shows the policy name");
assert.match(stageOptions, /task\.stage\.sessionReuseTaskMain/, "the dropdown must offer the reuse policies as its own labels");

// 5) 新建任务：可选 agent/模型覆盖下一个 agent 阶段
for (const key of ["agentOverride", "modelOverride", "effortOverride"]) {
  assert.match(appTask, new RegExp(`${key}\\?: string;`), `TaskInlineEditState must carry ${key}`);
}
// 建任务走 taskStagesForCreate：把它在**内置默认流水**的拷贝上应用面板覆盖。
// 模板库已下线，流水随包带上，后端不再回查任何模板。
assert.match(
  taskComposer,
  /taskStagesForCreate\(\s*defaultTaskFlowStages\(\),\s*\{/,
  "create-task must resolve the built-in flow and apply the panel overrides",
);
assert.match(
  appTask,
  /export function taskStagesForCreate\(/,
  "taskStagesForCreate must be the shared stage resolver",
);
assert.match(
  appTask,
  /if \(stage\.role !== "agent" \|\| touched\) return \{ \.\.\.stage \};/,
  "the override must stop after the first agent stage",
);
assert.match(appTask, /return next;/, "taskStagesForCreate always returns the full flow, never undefined");

// 5b) 新建任务面板的「立即执行」要跟随内置流水，并且能写回去。
const createPanel = inlinePanel.slice(inlinePanel.indexOf("const createTaskInputStage: StageTemplate"));
const createEditor = inlinePanel.slice(inlinePanel.indexOf("<StageEditor", inlinePanel.indexOf("createTaskInputStage")));
assert.match(
  createPanel,
  /start_immediately: taskInlineEdit\.startImmediately === true/,
  "the create-task stage must carry the start-immediately value the panel holds",
);
assert.match(
  createEditor,
  /isFirstStage\b/,
  "the create-task panel edits the first stage, so it must render the start-immediately chip",
);
// 附件「+」：面板底部的隐藏 file input 和缩略图 chips 一直都在，
// 只是没告诉编辑器有这个按钮可点（PromptEditor 的 + 只在传 onAttach 时渲染）。
// 任务详情面板早就这么传了，新建任务面板漏了 —— 钉住，防止又被悄悄删掉。
assert.match(
  createEditor,
  /onAttach=\{\(\) => taskInlineAttachmentInputRef\.current\?\.click\(\)\}/,
  "the create-task panel must pass onAttach so PromptEditor renders the add-attachment button",
);
assert.match(
  inlinePanel,
  /ref=\{taskInlineAttachmentInputRef\}\s*type="file"/,
  "the hidden file input the + button clicks must exist in the create-task panel",
);
// 面板的 agent 默认值直接读内置流水，不再有模板回退。
assert.match(
  inlinePanel,
  /const taskInlineTemplateAgent = firstAgentStage\(defaultTaskFlowStages\(\)\);/,
  "the agent stage must be read off the built-in flow",
);
assert.match(
  taskComposer,
  /startImmediately: stages\[0\]\?\.start_immediately === true/,
  "opening the create-task panel must seed start-immediately from the built-in flow's first stage",
);
assert.match(
  createEditor,
  /patch\.start_immediately !== undefined \? \{ startImmediately: patch\.start_immediately \} : \{\}/,
  "toggling the chip in the create-task panel must reach the edit state",
);
assert.match(
  taskComposer,
  /effort: edit\.effortOverride, startImmediately: edit\.startImmediately/,
  "create-task must send startImmediately through taskStagesForCreate",
);
assert.match(
  appTask,
  /if \(override\.startImmediately !== undefined && next\[0\]\) \{[\s\S]*?next\[0\] = \{ \.\.\.next\[0\], start_immediately: override\.startImmediately \};/,
  "taskStagesForCreate must write start-immediately onto the first (task input) stage",
);
// 面板上取消勾选必须压得住默认值：只在 true 时写的话，取消会退化成「照默认走」，
// 任务照样自己跑起来。
assert.doesNotMatch(
  appTask,
  /if \(override\.startImmediately && next\[0\]\)/,
  "start-immediately must be sent even when false",
);

// 5d) user 段的选项按语义裁掉：引擎对 user 段一律推进（不读 auto_advance），
//     也开不了 plan、更没有会话复用可言。摆出来只会是能撒谎的灰按钮。
//     首段改用「立即执行」——那才是真开关（建完要不要立刻开跑）。
assert.match(
  stageOptions,
  /startImmediately !== undefined \?/,
  "the start-immediately chip must render only when the stage says it is the first stage",
);
assert.match(
  stageEditor,
  /startImmediately=\{isFirstStage \? stage\.start_immediately === true : undefined\}/,
  "StageEditor must scope the chip to the first stage",
);
assert.match(
  stageEditor,
  /isFirstStage = false/,
  "StageEditor must take isFirstStage as a prop",
);
assert.doesNotMatch(panel, /isFirstStage=\{index === 0\}/, "the detail panel no longer renders stage editors");
assert.match(
  stageOptions,
  /: isAgent \? \([\s\S]*?task\.stage\.autoAdvance[\s\S]*?\) : null/,
  "auto-advance must be hidden on user stages: the engine advances them unconditionally",
);
assert.doesNotMatch(
  stageOptions,
  /disabled=\{disabled \|\| !isAgent/,
  "plan mode / session reuse must be hidden on user stages, not shown disabled",
);
// 服务端开跑只看首段的 start_immediately，绝不看 user 段的 auto_advance。
assert.match(
  serviceGo,
  /if first\.StartImmediately && len\(stages\) > 1 && strings\.TrimSpace\(in\.Input\) != "" \{/,
  "CreateTask must auto-start off the first stage's start_immediately",
);
assert.doesNotMatch(
  serviceGo,
  /if first\.AutoAdvance/,
  "CreateTask must not auto-start off the user stage's dead auto_advance field",
);


console.log("task-panel-unification.test.mjs: OK");
});
