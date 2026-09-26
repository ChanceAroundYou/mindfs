import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

const app = read("src/App.tsx");
const appTask = read("src/app/appTask.ts");
const dialog = read("src/components/TaskTemplateDialog.tsx");
const panel = read("src/components/TaskDetailPanel.tsx");
const select = read("src/components/Select.tsx");
const stageEditor = read("src/components/StageEditor.tsx");
const stageOptions = read("src/components/StageOptionsBar.tsx");
const panelShell = read("src/components/PanelShell.tsx");
const promptEditor = read("src/components/PromptEditor.tsx");
const css = read("src/index.css");
const boardView = read("src/components/TaskBoardView.tsx");
const serviceGo = fs.readFileSync(path.join(root, "../server/internal/kanban/service.go"), "utf8");
const templateStore = fs.readFileSync(path.join(root, "../server/internal/kanban/template_store.go"), "utf8");

// 三张面板深度统一：任务模板编辑（新建+编辑同一个组件）、任务详情、新建任务
// 都在编辑同一种东西（阶段定义 + prompt），以前各拼各的。
// 现在共用 StageEditor（一段阶段）+ PanelShell（弹窗外壳），只剩语义差异。

// 1) 三处都渲染共享组件
assert.match(dialog, /import \{ StageEditor \} from "\.\/StageEditor"/, "template dialog must use StageEditor");
assert.match(panel, /import \{ StageEditor \} from "\.\/StageEditor"/, "task detail must use StageEditor");
assert.match(app, /import \{ StageEditor \} from "\.\/components\/StageEditor"/, "create-task dialog must use StageEditor");
assert.match(dialog, /import \{ PanelShell,/, "template dialog must use PanelShell");
assert.match(panel, /import \{ PanelShell,/, "task detail must use PanelShell");

// 2) 弹窗外壳不再各手搓遮罩 / 阴影 / buttonStyle
for (const [name, src] of [["TaskTemplateDialog", dialog], ["TaskDetailPanel", panel], ["App create dialog", app]]) {
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
  app.slice(app.indexOf("<StageEditor")),
  /stageNamePlaceholder=/,
  "the create-task dialog must not render a stage name (that stage is always 任务输入)",
);
// user 段也能显示 agent 选择器（新建任务面板要选下一段的 agent/模型）
assert.match(promptEditor, /\{!isUser \|\| showAgentSelector \? \(/, "PromptEditor must allow forcing the selector on for user stages");
assert.match(app, /showAgentSelector=\{taskInlineHasAgentStage\}/, "the create-task editor must show the agent selector");

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
assert.match(
  app,
  /const overrideStages = applyStageOverride\(/,
  "create-task must go through the shared applyStageOverride",
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
const createPanel = app.slice(app.indexOf("const createTaskInputStage: StageTemplate"));
const createEditor = app.slice(app.indexOf("<StageEditor", app.indexOf("createTaskInputStage")));
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
assert.match(
  app,
  /const taskInlineTemplate = taskTemplates\.find\(\(tpl\) => tpl\.id === taskInlineEdit\.templateId\) \|\| null;/,
  "the create-task panel must look the picked template up once (agent + start-immediately both read it)",
);
assert.match(
  app,
  /const taskInlineTemplateAgent = firstAgentStage\(taskInlineTemplate\);/,
  "the agent stage must be read off that same lookup, not a second one",
);
assert.match(
  app,
  /startImmediately: template\?\.stages\?\.\[0\]\?\.snapshot\?\.start_immediately === true/,
  "opening the create-task panel must seed start-immediately from the template's first stage",
);
assert.match(
  app,
  /text: firstUserInputTemplate\(picked\), startImmediately: picked\.stages\?\.\[0\]\?\.snapshot\?\.start_immediately === true/,
  "switching templates must re-seed start-immediately, or the previous template's value sticks",
);
assert.match(
  createEditor,
  /patch\.start_immediately !== undefined \? \{ startImmediately: patch\.start_immediately \} : \{\}/,
  "toggling the chip in the create-task panel must reach the edit state",
);
assert.match(
  app,
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

// 5c) 面板上的字段说明、任务名输入框、user 开关：跟模板编辑面板一模一样。
assert.match(
  createEditor,
  /label=\{\(\s*<FieldLabelWithInfo/,
  "the create-task editor must reuse the template dialog's field label + info",
);
assert.match(
  app,
  /import \{ TaskTemplateDialog, FieldLabelWithInfo \} from "\.\/components\/TaskTemplateDialog"/,
  "the shared field label must be exported from the template dialog",
);
assert.doesNotMatch(
  createEditor,
  /onToggleRole=/,
  "the create-task panel keeps the user toggle greyed but visible, exactly like the template's first stage",
);
// 任务名必须和 user 开关同排。两者都在 StageEditor 内部才可能同排：任务名
// 曾经由 App.tsx 在 StageEditor 外面自己开一个 div，user 芯片被挤到下一排。
// 曾有过一次「只改宽度不改位置」的修法，样式对了、行还是错的。
assert.match(
  createEditor,
  /leading=\{\(\s*<input[\s\S]*?task\.namePlaceholder[\s\S]*?flex: "0 0 180px"/,
  "the task name must be handed to StageEditor's leading slot, styled like the stage name input",
);
assert.match(
  stageEditor,
  /\{leading\}\s*\n\s*\{stageNamePlaceholder \? \(/,
  "StageEditor must render leading in the same row as the stage name and the user toggle",
);
assert.doesNotMatch(
  stageEditor,
  /<div style=\{\{ display: "flex", alignItems: "center", gap: "6px", flexWrap: "wrap" ">\}\}>\s*\n\s*\{stageNamePlaceholder/,
  "leading must not be rendered in a row of its own",
);
assert.doesNotMatch(app, /flex: "1 1 auto", height: "30px"/, "the full-width task name input must be gone");

// 6) 下拉框全部自绘：应用内不再有任何原生 <select>
//    只看真正的 JSX 标签，注释里提到 <select> 不算（Select.tsx 的说明就写了）。
const selectSites = [];
for (const file of walk("src")) {
  if (!file.endsWith(".tsx")) continue;
  const code = read(file).replace(/\/\*[\s\S]*?\*\//g, "").replace(/\/\/[^\n]*/g, "");
  if (/<select[\s>]/.test(code)) selectSites.push(file);
}
assert.deepEqual(selectSites, [], `native <select> must be gone, still present in: ${selectSites.join(", ")}`);
assert.doesNotMatch(css, /\nselect \{/, "the global select rule must be deleted so no select silently misses styling");
assert.match(select, /createPortal\(renderMenu\(\), document\.body\)/, "Select must portal: the panels' bodies are overflow:auto and would clip an inline menu");
assert.match(select, /position: "fixed"/, "the portaled menu must be positioned fixed");
assert.match(select, /--menu-bg/, "the menu surface must follow the app menu background");
assert.match(select, /borderRadius: "12px"/, "the menu must match AgentSelector/ModeSelector's surface");
assert.doesNotMatch(stageOptions, /<select/, "the session-reuse dropdown must use Select, not a native select");
assert.match(app, /<Select/, "the create-task template dropdown must use Select");

// 7) 阶段选项四项常驻，不再藏三点菜单
for (const key of ["taskTemplate.autoAdvance", "taskTemplate.planMode", "taskTemplate.sessionReuse"]) {
  assert.ok(stageOptions.includes(`t("${key}")`), `the shared options bar must expose ${key}`);
}
assert.doesNotMatch(dialog, /StageOptionsMenu/, "the 3-dot menu must be gone from the template dialog");
assert.doesNotMatch(panel, /StageOptionsMenu/, "the 3-dot menu must be gone from the task detail");

// 保存时四个选项都要落库（UpdateStage 是全量替换，漏一个就丢一个）
for (const field of ["auto_advance: editAutoAdvance", "plan_mode:", "session_reuse_policy: editSessionReuse", "role: editRole"]) {
  assert.ok(panel.includes(field), `saveStage must persist ${field}`);
}


function walk(dir) {
  const out = [];
  for (const entry of fs.readdirSync(path.join(root, dir), { withFileTypes: true })) {
    const rel = `${dir}/${entry.name}`;
    if (entry.isDirectory()) out.push(...walk(rel));
    else out.push(rel);
  }
  return out;
}

// 8) 已执行过的阶段只可看：不可改、不可保存。三道门控各挡一路入口。
assert.match(
  panel,
  /const existingRun = latestStageRun\(detail, index\);\s*\n\s*if \(existingRun && String\(existingRun\.status\) !== "pending"\) return;/,
  "startEditStage must refuse an executed stage: no path may put it into edit mode",
);
assert.match(
  panel,
  /onEdit=\{editing \|\| executed \? undefined : \(\) => requestEditStage\(index\)\}/,
  "an executed stage must not offer the pencil",
);
assert.match(
  panel,
  /onSend=\{editing && !executed \? \(\) => void saveStage\(index\) : undefined\}/,
  "an executed stage must not offer save",
);
assert.match(
  panel,
  /const run = latestStageRun\(detail, index\);\s*\n\s*if \(run && String\(run\.status\) !== "pending"\) return;/,
  "saveStage must bail on an executed stage even if some future caller reaches it",
);
// 只读态仍然显示该段真实的选项值（不是隐藏、也不是全灰）
assert.match(stageEditor, /readOnly=\{mode !== "editable"\}/, "read mode shows option values, disabled");
assert.match(
  panel,
  /\{!executed && !isCurrent \? \(/,
  "an executed stage must not offer delete either",
);

// 9) 任务状态不能和会话 / worktree 绑死：会话没了、worktree 被删导致卡住的
//    任务，卡片上仍要能调整状态。曾经整排操作区都包在 !taskTerminal 里，
//    于是「已结束但状态不对」的任务连删除都点不到，只能干看着。
assert.match(
  boardView,
  /\{!taskTerminal \? \(\s*<>[\s\S]*?showTaskAdvanceButton[\s\S]*?taskCanComplete[\s\S]*?<\/>\s*\) : null\}/,
  "only run-now / complete are gated on taskTerminal",
);
assert.match(
  boardView,
  /handleMoveKanbanTask\(task, "cancel"\)[\s\S]{0,220}?<\/div>/,
  "the delete button must live OUTSIDE the !taskTerminal block",
);
assert.doesNotMatch(
  boardView,
  /\{!taskTerminal \? \(\s*<div style=\{\{ display: "flex", justifyContent: "flex-end"/,
  "the whole action row must no longer be hidden for terminal tasks",
);
// Cancel 本身不看 worktree / session：服务端末端任务也能取消，并清掉会话错误。
assert.match(
  serviceGo,
  /func \(s \*Service\) Cancel\(ctx context\.Context, in MoveInput\) \(TaskDetail, error\) \{\s*\n\s*return s\.setTaskStatus\(ctx, in\.RootID, in\.TaskID, StatusCancelled, "cancelled", in\.Reason, true\)/,
  "Cancel must not be gated on worktree or session state",
);
assert.match(
  serviceGo,
  /store\.UpdateTaskStatus\(ctx, taskID, status, nil, terminal\)/,
  "Cancel must clear the session error flag (nil aux) so a stuck task can be cleaned up",
);
