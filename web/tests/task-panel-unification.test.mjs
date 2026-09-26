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
const viteConfig = read("vite.config.ts");
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


// 11) 关闭面板前先问一句「是否放弃修改」。
//     三条关闭入口（遮罩 / 右上角 × / Esc）都必须过同一道判定，且判定只写一遍。
//     曾经三张面板各关各的，改到一半点外面就没了；后加确认又只在遮罩上生效，
//     右上角 × 和 Esc 照旧直关 —— 所以断言的是「三条路都走 requestClose」。
assert.match(
  panelShell,
  /hasUnsavedChanges\?: \(\) => boolean;/,
  "PanelShell must expose a dirty check so every panel shares one confirmation",
);
assert.match(
  panelShell,
  /const requestClose = useCallback\(\(\) => \{\s*\n\s*if \(!onClose\) return;\s*\n\s*if \(!hasUnsavedChanges\?\.\(\)\)/,
  "the guard must short-circuit straight to onClose when there is nothing to lose",
);
assert.match(
  panelShell,
  /confirmDialog\(\{\s*\n\s*message: discardConfirmMessage \|\| t\("common\.discardChangesConfirm"\),/,
  "a dirty close must confirm, with a caller-overridable message",
);
assert.match(
  panelShell,
  /if \(ok\) onClose\(\);/,
  "the panel may only close after the user confirms discarding",
);
assert.match(
  panelShell,
  /closeOnOverlayClick && event\.target === event\.currentTarget\) requestClose\(\)/,
  "the overlay must go through the guard, not call onClose directly",
);
assert.match(
  panelShell,
  /document\.addEventListener\("keydown", onKeyDown, true\)/,
  "Esc must be captured, or the editors inside swallow it before it bubbles",
);
assert.match(
  panelShell,
  /if \(event\.key !== "Escape"\) return;[\s\S]*?requestClose\(\);/,
  "Esc must route through the same guard",
);
// headerRight 收函数而不是收节点：收节点的话调用方写 onClick={onClose} 就绕过了确认。
assert.match(
  panelShell,
  /headerRight\?: \(requestClose: \(\) => void\) => React\.ReactNode;/,
  "headerRight must hand the guarded close down to callers",
);
assert.match(
  panelShell,
  /\{headerRight\?\.\(requestClose\) \?\? null\}/,
  "PanelShell must invoke headerRight with its guarded close",
);
assert.doesNotMatch(
  panelShell,
  /\{headerRight\}/,
  "rendering headerRight as a bare node would drop the guard",
);
for (const [name, src] of [
  ["TaskTemplateDialog", dialog],
  ["TaskDetailPanel", panel],
  ["App create dialog", app],
]) {
  assert.doesNotMatch(
    src,
    /onClick=\{onClose\}/,
    `${name} must not close via a bare onClick={onClose} — that skips the discard confirm`,
  );
}
assert.match(
  dialog,
  /onClick=\{requestClose\} style=\{panelButtonStyle\("secondary"\)\}/,
  "the template dialog's close button must use the guarded close",
);
// 三张面板都要真的接上 dirty 判定，否则守卫对它是空转
assert.match(dialog, /hasUnsavedChanges=\{\(\) => dirty\}/, "the template dialog must report an edited template");
assert.match(
  dialog,
  /const dirty = JSON\.stringify\(draft\) !== baselineRef\.current;/,
  "dirty must compare against the snapshot taken when the dialog opened, not a baseline recomputed each render",
);
assert.match(
  panel,
  /hasUnsavedChanges=\{\(\) => editingName && nameDraft\.trim\(\) !== \(task\.name \|\| ""\)\.trim\(\)\}/,
  "the detail panel must only warn when a rename is actually mid-flight",
);
assert.match(
  app,
  /hasUnsavedChanges=\{\(\) => String\(taskInlineEdit\?\.text \|\| ""\)\.trim\(\) !== ""\}/,
  "the create-task panel must warn when a prompt was typed",
);
// 取消按钮被去掉，改成「点外面关闭」；可见的关闭入口只剩右上角 ×
assert.doesNotMatch(
  app.slice(app.indexOf("<PanelShell", app.indexOf("createTaskInputStage"))),
  /t\("common\.cancel"\)/,
  "the create-task panel must not keep a cancel button",
);
for (const [name, src] of [
  ["TaskTemplateDialog", dialog],
  ["TaskDetailPanel", panel],
  ["App create dialog", app],
]) {
  assert.match(
    src,
    /<CloseGlyph \/>/,
    `${name} must render the shared × so the panel is still visibly closable`,
  );
}

// 12) 新建任务面板不再有底部大片空白：去掉 minHeight 后整卡按内容收缩。
//     minHeight 一撤，卡片只剩 maxHeight，卡高就由内容决定；遮罩是
//     alignItems:center，居中不受影响。
assert.doesNotMatch(
  app.slice(app.indexOf("<PanelShell", app.indexOf("createTaskInputStage"))),
  /minHeight=/,
  "the create-task panel must not pin a minHeight — that was the empty band at the bottom",
);
assert.match(
  panelShell,
  /\.\.\.\(minHeight \? \{ minHeight \} : \{\}\)/,
  "PanelShell must treat minHeight as opt-in so omitting it really collapses the card",
);
assert.doesNotMatch(
  dialog,
  /minHeight=\{520\}/,
  "the template dialog's fixed 520px floor had the same dead-space problem",
);
assert.match(
  panelShell,
  /alignItems: "center"/,
  "the overlay must keep centering the card, or a collapsed panel drifts to the top",
);

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
//    标题行/操作行已抽进 TaskCardRows（看板和工作台共用），契约跟着文件走。
const cardRows = read("src/components/TaskCardRows.tsx");
assert.match(
  cardRows,
  /\{!terminal \? \(\s*<>[\s\S]*?showAdvance[\s\S]*?canComplete[\s\S]*?<\/>\s*\) : null\}/,
  "only run-now / complete are gated on taskTerminal",
);
assert.match(
  cardRows,
  /onMove\(task, "cancel"\)[\s\S]{0,220}?<\/div>/,
  "the delete button must live OUTSIDE the !terminal block",
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

// 10) Service Worker 缓存策略：index.html 是整条缓存链的根。
//     一旦把它 cache-first 住，旧标签页会一直拿旧壳子去请求早已不存在的
//     旧 chunk（内容哈希对不上），表现为一整批过期 js 反复加载。
//     静态资源则改 stale-while-revalidate，让哈希收敛自动发生。
const navHandler = viteConfig.slice(
  viteConfig.indexOf("async function handleNavigationRequest"),
  viteConfig.indexOf("async function handleStaticRequest"),
);
assert.doesNotMatch(
  navHandler,
  /cache\.put\(INDEX_URL/,
  "index.html must never be written back into the cache: it is the root of the whole asset chain",
);
assert.match(
  navHandler,
  /return await fetch\(request\);/,
  "navigation must go straight to the network, falling back to the precached shell when offline",
);
const staticHandler = viteConfig.slice(
  viteConfig.indexOf("async function handleStaticRequest"),
  viteConfig.indexOf("function revalidate("),
);
assert.match(
  staticHandler,
  /cachedRuntimeResponse[\s\S]{0,160}?revalidate\(request, runtimeCache\);/,
  "cached runtime assets must revalidate in the background instead of being served stale forever",
);
assert.match(
  viteConfig,
  /function revalidate\(request, cache\) \{[\s\S]*?fetch\(request\)[\s\S]*?cache\.put\(/,
  "revalidate must refresh the cached copy in the background",
);

// 10) 任务状态色只有一份定义（appTask.taskStatusColor），看板卡 / 工作台卡 /
//     详情面板都从它拿。以前详情面板自带一份 statusColors，工作台干脆没有配色，
//     于是同一个「已完成」在三处是三种颜色。色值走 --status-* token 而不是字面 hex，
//     深浅主题（含 meadow/moss）才能自动跟随。
assert.match(
  appTask,
  /export function taskStatusColor\(status: string\): string \{[\s\S]*?return colors\[status\] \|\| "var\(--text-secondary\)";/,
  "one helper maps task status to a color, with a neutral fallback",
);
for (const [status, color] of [
  ["success", "var(--status-ok)"],
  ["approved", "var(--status-ok)"],
  ["fail", "var(--status-bad)"],
  ["rejected", "var(--status-bad)"],
  ["waiting_user", "var(--status-warn)"],
  ["running", "var(--accent-color)"],
  ["queued", "var(--accent-color)"],
  ["pending", "var(--text-secondary)"],
  ["paused", "var(--text-secondary)"],
  ["cancelled", "var(--text-secondary)"],
]) {
  assert.match(
    appTask,
    new RegExp(`\\b${status}: "${color.replace(/[()]/g, "\\$&")}"`),
    `${status} must map to ${color}`,
  );
}
assert.doesNotMatch(
  appTask,
  /#[0-9a-fA-F]{3,8}\b/,
  "the status helper must not hardcode hex — light/dark themes each supply their own value",
);
assert.match(panel, /taskStatusColor\(task\.status\)/, "the detail panel reads the shared status color");
assert.match(panel, /taskStatusColor\(run\?\.status \|\| ""\)/, "so does the per-stage run badge");
assert.doesNotMatch(
  panel,
  /const statusColors: Record<string, string>/,
  "the detail panel's private statusColors map is gone — one definition, not two",
);
for (const token of ["--status-ok", "--status-warn", "--status-bad"]) {
  assert.ok(css.includes(`${token}:`), `${token} must be defined in index.css`);
  // 浅色 :root + 深色 data-theme + prefers-color-scheme 三个块都得给值
  const occurrences = css.split(`${token}:`).length - 1;
  assert.ok(occurrences >= 3, `${token} needs a light value plus both dark blocks, found ${occurrences}`);
}
