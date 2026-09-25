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
  "the create-task dialog must not render a stage name (that stage is always 任务初始输入)",
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
