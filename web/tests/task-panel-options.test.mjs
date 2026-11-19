import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";

// 面板上的交互与文案：下拉自绘、关闭前确认、底部空白、移动端软键盘。
// 阶段定义/阶段选项已随会话模型删除，这里不再有 StageEditor / StageOptionsBar。

test("task-panel-options", async () => {
const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

const panel = read("src/components/task/TaskDetailPanel.tsx");
const select = read("src/components/Select.tsx");
const panelShell = read("src/components/PanelShell.tsx");
const panelCloseHook = read("src/hooks/usePanelShellClose.ts");
const appRootView = read("src/app/AppRootView.tsx");
const inlinePanel = read("src/components/app/TaskInlineEditPanel.tsx");
const css = read("src/index.css");
const createPanel = inlinePanel.slice(inlinePanel.indexOf("<PanelShell"));

// 下拉框全部自绘：应用内不再有任何原生 <select>
// 只看真正的 JSX 标签，注释里提到 <select> 不算。
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
assert.match(inlinePanel, /<Select/, "the create-task panel dropdowns must use Select");

// 关闭面板前先问一句「是否放弃修改」。
assert.match(panelShell, /hasUnsavedChanges\?: \(\) => boolean;/, "PanelShell must expose a dirty check so every panel shares one confirmation");
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
assert.match(panelCloseHook, /if \(ok\) onClose\(\);/, "the panel may only close after the user confirms discarding");
assert.match(panelShell, /if \(event\.target === event\.currentTarget\) requestClose\(\);/, "the overlay must be the one close path, going through the guard");
assert.doesNotMatch(panelShell, /closeOnOverlayClick/, "closeOnOverlayClick is dead — the overlay always closes");
assert.doesNotMatch(panelShell, /CloseGlyph/, "the shared × must be gone entirely");
assert.doesNotMatch(panelShell, /onKeyDown|"Escape"|keydown/, "Esc must no longer close the panel");
for (const [name, src] of [
  ["PanelShell", panelShell],
  ["TaskDetailPanel", panel],
  ["App create dialog", appRootView],
]) {
  assert.doesNotMatch(src, /onClick=\{onClose\}|onClick=\{requestClose\}/, `${name} must not close via a click handler — with no explicit close control, the overlay is the only route`);
  assert.doesNotMatch(src, /aria-label=\{t\("common\.close"\)\}/, `${name} must not render an explicit close affordance`);
}
assert.doesNotMatch(createPanel, /t\("common\.cancel"\)/, "the create-task panel must not keep a cancel button");
assert.match(panelShell, /headerRight\?: React\.ReactNode;/, "headerRight is a plain node again");
assert.match(panelShell, /\{headerRight\}/, "PanelShell must render headerRight directly");
assert.match(
  panel,
  /hasUnsavedChanges=\{\(\) => editingName && nameDraft\.trim\(\) !== \(task\.name \|\| ""\)\.trim\(\)\}/,
  "the detail panel must only warn when a rename is actually mid-flight",
);
assert.match(panel, /title=\{editingName \? \(\s*<input/, "the rename input must take over the title slot, not sit beside it");
assert.doesNotMatch(panel, /headerLeft=\{editingName \? \(\s*<input/, "the rename input must not live in headerLeft — that is what left the stale name on screen");
assert.doesNotMatch(panel, /headerLeft=\{editingName \?[\s\S]{0,400}?taskStatusColor\(task\.status\)/, "the status/#number badges are not the cell being edited — keep them out of the editing ternary");
assert.match(panel, /title=\{editingName \?[\s\S]{0,1200}?task\.name \|\| t\("task\.namePlaceholder"\)/, "the read-only name must stay in the title slot, shown only when not editing");
assert.match(
  inlinePanel,
  /hasUnsavedChanges=\{\(\) => String\(taskInlineEdit\?\.text \|\| ""\)\.trim\(\) !== ""\}/,
  "the create-task panel must warn when a prompt was typed",
);

// 新建任务面板不再有底部大片空白：去掉 minHeight 后整卡按内容收缩。
assert.doesNotMatch(createPanel, /minHeight=/, "the create-task panel must not pin a minHeight — that was the empty band at the bottom");
assert.match(panelShell, /\.\.\.\(minHeight \? \{ minHeight \} : \{\}\)/, "PanelShell must treat minHeight as opt-in so omitting it really collapses the card");
assert.match(panelShell, /alignItems: "center"/, "the overlay must keep centering the card, or a collapsed panel drifts to the top");

// 移动端软键盘不能盖住面板。
assert.doesNotMatch(panelShell, /inset: 0,/, "the overlay must not be pinned with inset:0 — that leaves it under the keyboard");
assert.match(panelShell, /const \[viewportHeight, setViewportHeight\] = useState\(0\);/, "PanelShell must track the visual viewport height");
assert.match(panelShell, /const vv = window\.visualViewport;/, "the tracked viewport must be visualViewport, not window.innerHeight");
assert.match(panelShell, /vv\.addEventListener\("resize", sync\);[\s\S]*?vv\.addEventListener\("scroll", sync\);/, "the keyboard raising/resizing and any viewport scroll must both re-sync the height");
assert.match(panelShell, /"--panel-vh": `\$\{viewportHeight\}px`/, "the measured height must reach the card's max-height via --panel-vh");
assert.match(panelShell, /maxHeight: "min\(88dvh, calc\(var\(--panel-vh, 100dvh\) - 48px\)\)"/, "the card's height cap must follow the visible viewport, not just the layout viewport");
assert.match(panelShell, /height: viewportHeight \? `\$\{viewportHeight\}px` : "100dvh",/, "the overlay's own height must follow the visible viewport too, or the card stays centered against the wrong box");
const mobileBlock = panelShell.slice(panelShell.indexOf("@media (max-width: 640px)"));
assert.match(mobileBlock, /align-items: flex-start !important;/, "on mobile the card anchors to the top so it floats above the keyboard instead of being centered away");
assert.match(
  mobileBlock,
  /max-height: calc\(var\(--panel-vh, 100dvh\) - 56px\) !important;/,
  "the mobile height cap must read --panel-vh and subtract the top bar + padding, or the bottom of a long panel (the run view) sits outside the visible area",
);
assert.match(mobileBlock, /overscroll-behavior: contain;/, "the scrolling body must not chain scrolls to the page behind the panel");

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
