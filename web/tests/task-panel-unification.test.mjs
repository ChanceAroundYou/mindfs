import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";
import { readGoPackage } from "./go-source.mjs";

// 三张面板深度统一：任务模板编辑 / 任务详情 / 新建任务都编辑同一种东西
// （阶段定义 + prompt），共用 StageEditor + PanelShell，只剩语义差异。
// 字段说明与交互细节见 task-panel-options；已执行阶段只读、状态不绑会话、
// SW 缓存与状态色见 task-card-status。

test("task-panel-unification", async () => {
const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

const dialog = read("src/components/task/TaskTemplateDialog.tsx");
const panel = read("src/components/task/TaskDetailPanel.tsx");
// 新建任务面板（TaskInlineEditPanel）与 AppRootView 都不在虚拟 "src/App.tsx" 里，直接读真文件。
const inlinePanel = read("src/components/app/TaskInlineEditPanel.tsx");
const appRootView = read("src/app/AppRootView.tsx");
const panelShell = read("src/components/PanelShell.tsx");
const stageEditor = read("src/components/StageEditor.tsx");
const stageOptions = read("src/components/StageOptionsBar.tsx");
const promptEditor = read("src/components/PromptEditor.tsx");
const appTask = read("src/app/appTask.ts");
const taskComposer = read("src/app/useTaskComposer.ts");
// Go 侧按**包**读，不钉文件名：2026-10-16 的 B1–B10 拆分把 service.go /
// service_tasks.go 切成职责文件（service_run_moves.go、service_create.go…），
// 实现没少、只是搬了家。
const serviceGo = readGoPackage("server/internal/kanban");
const templateStore = serviceGo;

// 1) 三处都渲染共享组件
assert.match(dialog, /import \{ StageEditor \} from "\.\/StageEditor"/, "template dialog must use StageEditor");
assert.match(panel, /import \{ StageEditor \} from "\.\/StageEditor"/, "task detail must use StageEditor");
// 新建任务面板是 TaskInlineEditPanel，现在由 AppRootView 渲染（App.tsx 只剩四层聚合调用）。
assert.match(inlinePanel, /import \{ StageEditor \} from "\.\.\/task\/StageEditor"/, "create-task panel must use StageEditor");
assert.match(dialog, /import \{ PanelShell,/, "template dialog must use PanelShell");
assert.match(panel, /import \{ PanelShell,/, "task detail must use PanelShell");

// 2) 弹窗外壳不再各手搓遮罩 / 阴影 / buttonStyle
for (const [name, src] of [["TaskTemplateDialog", dialog], ["TaskDetailPanel", panel], ["App create dialog", appRootView]]) {
  assert.doesNotMatch(
    src,
    /rgba\(15, 23, 42, 0\.36\)/,
    `${name} must not hand-roll its own overlay; PanelShell owns it`,
  );
}

assert.doesNotMatch(dialog, /function buttonStyle/, "the duplicate buttonStyle in TaskTemplateDialog must be gone");
assert.doesNotMatch(panel, /function buttonStyle/, "the duplicate buttonStyle in TaskDetailPanel must be gone");
assert.match(panelShell, /export function panelButtonStyle/, "PanelShell owns the shared button style");
assert.match(panelShell, /zIndex: 90/, "PanelShell must pin one z-index for all three panels");

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
assert.match(dialog, /onToggleRole=\{index === 0 \? undefined : onToggleStageRole\(index\)\}/, "the template's first stage locks its role");
assert.match(panel, /onToggleRole=\{editing \?/, "the detail panel offers a role toggle while editing");
// 会话复用：不再有「会话复用」前缀文字，收起态直接显示当前策略
assert.doesNotMatch(stageOptions, /\{t\("taskTemplate\.sessionReuse"\)\}<\/span>/, "the session-reuse label must be gone; the dropdown shows the policy name");
assert.match(stageOptions, /taskTemplate\.sessionReuseTaskMain/, "the dropdown must offer the reuse policies as its own labels");

// 5) 新建任务：可选 agent/模型覆盖下一个 agent 阶段
for (const key of ["agentOverride", "modelOverride", "effortOverride"]) {
  assert.match(appTask, new RegExp(`${key}\\?: string;`), `TaskInlineEditState must carry ${key}`);
}
// 建任务走 taskStagesForCreate（它在 applyStageOverride 之上多兜一层：
// 模板库只在主节点，没覆盖时也必须把流水随包带上，否则后端在 worker 节点上
// 回查不到模板会整个建不出任务）。共享的覆盖逻辑仍是同一个 applyStageOverride。
assert.match(
  taskComposer,
  /const createStages = taskStagesForCreate\(/,
  "create-task must go through the shared stage resolver",
);
assert.match(
  appTask,
  /const overridden = applyStageOverride\(template, override\);/,
  "taskStagesForCreate must build on applyStageOverride, not re-implement the override",
);
assert.match(
  appTask,
  /export function applyStageOverride\(/,
  "applyStageOverride must be the shared helper (template + create-task both use it)",
);
assert.match(
  appTask,
  /if \(snapshot\?\.role !== "agent"\) return snapshot;\s*\n\s*if \(touched\) return snapshot;/,
  "the override must stop after the first agent stage",
);
assert.match(appTask, /return touched \? stages : undefined/, "no agent stage means no override is sent");

// 5b) 新建任务面板的「立即执行」要跟随模板，并且能写回去。
//     曾经 createTaskInputStage 只带 agent/model/effort，首段压根没读模板，
//     于是「建完要不要自己跑」在面板上恒为灭、也不跟模板走。
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
assert.match(
  inlinePanel,
  /const taskInlineTemplate = taskTemplates\.find\(\(tpl\) => tpl\.id === taskInlineEdit\.templateId\) \|\| null;/,
  "the create-task panel must look the picked template up once (agent + start-immediately both read it)",
);
assert.match(
  inlinePanel,
  /const taskInlineTemplateAgent = firstAgentStage\(taskInlineTemplate\);/,
  "the agent stage must be read off that same lookup, not a second one",
);
assert.match(
  taskComposer,
  /startImmediately: template\?\.stages\?\.\[0\]\?\.snapshot\?\.start_immediately === true/,
  "opening the create-task panel must seed start-immediately from the template's first stage",
);
assert.match(
  inlinePanel,
  /text: firstUserInputTemplate\(picked\), startImmediately: picked\.stages\?\.\[0\]\?\.snapshot\?\.start_immediately === true/,
  "switching templates must re-seed start-immediately, or the previous template's value sticks",
);
assert.match(
  createEditor,
  /patch\.start_immediately !== undefined \? \{ startImmediately: patch\.start_immediately \} : \{\}/,
  "toggling the chip in the create-task panel must reach the edit state",
);
assert.match(
  taskComposer,
  /effort: edit\.effortOverride, startImmediately: edit\.startImmediately/,
  "create-task must send startImmediately through applyStageOverride",
);
assert.match(
  appTask,
  /if \(override\.startImmediately !== undefined && stages\[0\]\) \{[\s\S]*?stages\[0\] = \{ \.\.\.stages\[0\], start_immediately: override\.startImmediately \};[\s\S]*?return touched \? stages : undefined;/,
  "applyStageOverride must write start-immediately onto the first (task input) stage",
);
// 面板上取消勾选必须压得住模板：只在 true 时写的话，取消会退化成「照模板走」，
// 任务照样自己跑起来。面板开出来就填了模板的值，所以这里永远要照实发。
assert.doesNotMatch(
  appTask,
  /if \(override\.startImmediately && stages\[0\]\)/,
  "start-immediately must be sent even when false, or unchecking a template-checked chip is a no-op",
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
assert.match(dialog, /isFirstStage=\{index === 0\}/, "the template's first stage gets the start-immediately chip");
assert.match(panel, /isFirstStage=\{index === 0\}/, "the detail panel's first stage gets it too");
assert.match(
  stageOptions,
  /: isAgent \? \([\s\S]*?taskTemplate\.autoAdvance[\s\S]*?\) : null/,
  "auto-advance must be hidden on user stages: the engine advances them unconditionally",
);
assert.doesNotMatch(
  stageOptions,
  /disabled=\{disabled \|\| !isAgent/,
  "plan mode / session reuse must be hidden on user stages, not shown disabled",
);
// 存模板时把 user 段的 auto_advance 归一成 true：那个字段引擎不读，
// 面板上也改不了，存成 false 只是让 JSON 里躺着个假值。存量模板一存就自愈。
assert.match(
  templateStore,
  /Snapshot\.Role == RoleUser \{[\s\S]{0,200}?Snapshot\.AutoAdvance = true/,
  "saving a template must normalize user stages' auto_advance to true",
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
