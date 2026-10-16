import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

// 面板上的交互与文案：字段说明、任务名同排、下拉自绘、阶段选项常驻、
// 关闭前确认、底部空白、移动端软键盘。

test("task-panel-options", async () => {
const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

const dialog = read("src/components/task/TaskTemplateDialog.tsx");
const panel = read("src/components/task/TaskDetailPanel.tsx");
const panelHook = read("src/components/task/useTaskDetailPanel.ts");
const select = read("src/components/Select.tsx");
const stageEditor = read("src/components/StageEditor.tsx");
const stageOptions = read("src/components/StageOptionsBar.tsx");
const panelShell = read("src/components/PanelShell.tsx");
const panelCloseHook = read("src/hooks/usePanelShellClose.ts");
const appRootView = read("src/app/AppRootView.tsx");
// 新建任务面板（TaskInlineEditPanel）与 AppRootView 都不在虚拟 "src/App.tsx" 里，直接读真文件。
const inlinePanel = read("src/components/app/TaskInlineEditPanel.tsx");
const css = read("src/index.css");
// 新建面板里「阶段定义」与「阶段编辑器」两段切片（5b 与 5c 共用同一对切片）。
const createPanel = inlinePanel.slice(inlinePanel.indexOf("const createTaskInputStage: StageTemplate"));
const createEditor = inlinePanel.slice(inlinePanel.indexOf("<StageEditor", inlinePanel.indexOf("createTaskInputStage")));

// 5c) 面板上的字段说明、任务名输入框、user 开关：跟模板编辑面板一模一样。
assert.match(
  createEditor,
  /label=\{\(\s*<FieldLabelWithInfo/,
  "the create-task editor must reuse the template dialog's field label + info",
);
// TaskTemplateDialog 由 AppRootView 渲染（App.tsx 只剩四层聚合调用）；AppRootView 在 src/app/，
// 所以它眼里的路径是 ../components/task/。
assert.match(
  appRootView,
  /import \{ TaskTemplateDialog, FieldLabelWithInfo \} from "\.\.\/components\/task\/TaskTemplateDialog"/,
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
assert.doesNotMatch(inlinePanel, /flex: "1 1 auto", height: "30px"/, "the full-width task name input must be gone");

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
assert.match(inlinePanel, /<Select/, "the create-task template dropdown must use Select");

// 7) 阶段选项四项常驻，不再藏三点菜单
for (const key of ["taskTemplate.autoAdvance", "taskTemplate.planMode", "taskTemplate.sessionReuse"]) {
  assert.ok(stageOptions.includes(`t("${key}")`), `the shared options bar must expose ${key}`);
}
assert.doesNotMatch(dialog, /StageOptionsMenu/, "the 3-dot menu must be gone from the template dialog");
assert.doesNotMatch(panel, /StageOptionsMenu/, "the 3-dot menu must be gone from the task detail");

// 保存时四个选项都要落库（UpdateStage 是全量替换，漏一个就丢一个）
for (const field of ["auto_advance: editAutoAdvance", "plan_mode:", "session_reuse_policy: editSessionReuse", "role: editRole"]) {
  assert.ok(panelHook.includes(field), `saveStage must persist ${field}`);
}


// 11) 关闭面板前先问一句「是否放弃修改」。
//     关闭入口只剩「点空白（遮罩）」一条：取消键、右上角 ×、Esc 全部拿掉，
//     所以判定天然只有一个出口，不存在「某条路忘了加确认」。
assert.match(
  panelShell,
  /hasUnsavedChanges\?: \(\) => boolean;/,
  "PanelShell must expose a dirty check so every panel shares one confirmation",
);
assert.match(
  panelCloseHook,
  /if \(!onClose\) return;\s*\n\s*if \(!hasUnsavedChanges\?\.\(\)\)/,
  "the guard must short-circuit straight to onClose when there is nothing to lose",
);
assert.match(
  panelCloseHook,
  /confirmDialog\(\{\s*\n\s*message: t\("common\.discardChangesConfirm"\),/,
  "a dirty close must confirm before discarding",
);
assert.match(
  panelCloseHook,
  /if \(ok\) onClose\(\);/,
  "the panel may only close after the user confirms discarding",
);
assert.match(
  panelShell,
  /if \(event\.target === event\.currentTarget\) requestClose\(\);/,
  "the overlay must be the one close path, going through the guard",
);
// 显式关闭控件一律退场：留一个就直接关，等于把「误点丢内容」的坑又挖回来。
assert.doesNotMatch(panelShell, /closeOnOverlayClick/, "closeOnOverlayClick is dead — the overlay always closes");
assert.doesNotMatch(panelShell, /CloseGlyph/, "the shared × must be gone entirely");
assert.doesNotMatch(panelShell, /onKeyDown|"Escape"|keydown/, "Esc must no longer close the panel");
// 关闭控件可能长在三处里的任何一处：共享 header，或某个调用方自定义的 headerRight。
// 只查「有没有 CloseGlyph」太窄 —— 换个画法或直接写个 onClick 就溜过去了。
for (const [name, src] of [
  ["PanelShell", panelShell],
  ["TaskTemplateDialog", dialog],
  ["TaskDetailPanel", panel],
  ["App create dialog", appRootView],
]) {
  assert.doesNotMatch(
    src,
    /onClick=\{onClose\}|onClick=\{requestClose\}/,
    `${name} must not close via a click handler — with no explicit close control, the overlay is the only route`,
  );
  assert.doesNotMatch(
    src,
    /aria-label=\{t\("common\.close"\)\}|taskTemplate\.close/,
    `${name} must not render an explicit close affordance`,
  );
}
assert.doesNotMatch(
  inlinePanel.slice(inlinePanel.indexOf("<PanelShell", inlinePanel.indexOf("createTaskInputStage"))),
  /t\("common\.cancel"\)/,
  "the create-task panel must not keep a cancel button",
);
// headerRight 回到收节点：不再有任何「受控关闭」要发下去
assert.match(panelShell, /headerRight\?: React\.ReactNode;/, "headerRight is a plain node again");
assert.match(panelShell, /\{headerRight\}/, "PanelShell must render headerRight directly");
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
// 改名时输入框占住 name 那一格，旧名跟着 unmount。PanelShell 的 header 是
// [headerLeft][title][headerRight] 三个无条件兄弟，输入框放 headerLeft、name 放
// title（且不跟着 editingName 走）就会并排显示「输入框 + 旧名」。
assert.match(
  panel,
  /title=\{editingName \? \(\s*<input/,
  "the rename input must take over the title slot, not sit beside it",
);
assert.doesNotMatch(
  panel,
  /headerLeft=\{editingName \? \(\s*<input/,
  "the rename input must not live in headerLeft — that is what left the stale name on screen",
);
assert.doesNotMatch(
  panel,
  /headerLeft=\{editingName \?[\s\S]{0,400}?taskStatusColor\(task\.status\)/,
  "the status/template/#number badges are not the cell being edited — keep them out of the editing ternary",
);
assert.match(
  panel,
  /title=\{editingName \?[\s\S]{0,1200}?task\.name \|\| t\("task\.namePlaceholder"\)/,
  "the read-only name must stay in the title slot, shown only when not editing",
);
assert.match(
  inlinePanel,
  /hasUnsavedChanges=\{\(\) => String\(taskInlineEdit\?\.text \|\| ""\)\.trim\(\) !== ""\}/,
  "the create-task panel must warn when a prompt was typed",
);

// 12) 新建任务面板不再有底部大片空白：去掉 minHeight 后整卡按内容收缩。
//     minHeight 一撤，卡片只剩 maxHeight，卡高就由内容决定；遮罩是
//     alignItems:center，居中不受影响。
assert.doesNotMatch(
  inlinePanel.slice(inlinePanel.indexOf("<PanelShell", inlinePanel.indexOf("createTaskInputStage"))),
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

// 13) 移动端软键盘不能盖住面板。关键在两个地方：
//     遮罩不能是 inset:0 —— 键盘弹出时 layout 视口纹丝不动，inset:0 会把
//     遮罩和卡片一起留在键盘下面；高度必须取 visualViewport。
//     移动端媒体查询里 alignItems 换成 flex-start，上浮幅度由 --panel-vh 决定。
assert.doesNotMatch(
  panelShell,
  /inset: 0,/,
  "the overlay must not be pinned with inset:0 — that leaves it under the keyboard",
);
assert.match(
  panelShell,
  /const \[viewportHeight, setViewportHeight\] = useState\(0\);/,
  "PanelShell must track the visual viewport height",
);
assert.match(
  panelShell,
  /const vv = window\.visualViewport;/,
  "the tracked viewport must be visualViewport, not window.innerHeight",
);
assert.match(
  panelShell,
  /vv\.addEventListener\("resize", sync\);[\s\S]*?vv\.addEventListener\("scroll", sync\);/,
  "the keyboard raising/resizing and any viewport scroll must both re-sync the height",
);
assert.match(
  panelShell,
  /"--panel-vh": `\$\{viewportHeight\}px`/,
  "the measured height must reach the card's max-height via --panel-vh",
);
assert.match(
  panelShell,
  /maxHeight: "min\(88dvh, calc\(var\(--panel-vh, 100dvh\) - 48px\)\)/,
  "the card's height cap must follow the visible viewport, not just the layout viewport",
);
assert.match(
  panelShell,
  /height: viewportHeight \? `\$\{viewportHeight\}px` : "100dvh",/,
  "the overlay's own height must follow the visible viewport too, or the card stays centered against the wrong box",
);
const mobileBlock = panelShell.slice(
  panelShell.indexOf("@media (max-width: 640px)"),
);
assert.match(
  mobileBlock,
  /align-items: flex-start !important;/,
  "on mobile the card anchors to the top so it floats above the keyboard instead of being centered away",
);
assert.match(
  mobileBlock,
  /max-height: var\(--panel-vh, 60dvh\) !important;/,
  "the mobile height cap must read --panel-vh, so it shrinks as the keyboard rises",
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


console.log("task-panel-options.test.mjs: OK");
});
