import assert from "node:assert/strict";
import fs from "node:fs";

const read = (file) => fs.readFileSync(new URL(`../src/${file}`, import.meta.url), "utf8");
const nodeRegistry = read("services/net/nodeRegistry.ts");
const defaultList = read("components/file/DefaultListView.tsx");
const fileTree = read("components/file/FileTree.tsx");
const fileTreeEntries = read("components/file/fileTreeEntries.tsx");
const sessionList = read("components/session/SessionList.tsx");
const sessionCard = read("components/session/useSessionCard.ts");
const app = read("App.tsx");
const appStateLayer = read("app/useAppStateLayer.ts");
const appProjectDisplay = read("app/useAppProjectDisplay.ts");
const sessionPane = read("components/app/SessionPane.tsx");
const vite = fs.readFileSync(new URL("../vite.config.ts", import.meta.url), "utf8");
const css = read("index.css");
const agentSelector = read("components/agent/AgentSelector.tsx");
const mainViewSwitcher = read("components/shell/MainViewSwitcher.tsx");
const rootBadgeStyle = read("shared/rootBadgeStyle.ts");

assert.match(nodeRegistry, /PALETTE = \["#3b82f6", "#f59e0b", "#7c6bd6"/);
assert.match(nodeRegistry, /const PREVIOUS_PALETTE = \["#7c6bd6", "#c9b84a"/);
assert.match(nodeRegistry, /const OLD_PALETTE = \["#6d5bcf", "#0ea5a0"/);
// 当前调色板保护：已属于 PALETTE 的存储色（尤其 #7c6bd6）不得被迁移改写
assert.match(
  nodeRegistry,
  /PALETTE\.some\(\(c\) => c\.toLowerCase\(\) === color\.toLowerCase\(\)\)/,
);
// paletteIndex 覆盖 PREVIOUS_PALETTE 与 OLD_PALETTE 两个历史调色板
assert.match(nodeRegistry, /const paletteIndex = \[PREVIOUS_PALETTE, OLD_PALETTE\]/);
assert.match(defaultList, /rootColor\?: string/);
assert.match(defaultList, /rootBadgeButtonStyle/);
// project-home 徽标在 FileViewer / Breadcrumbs / GitDiffViewer 里（DefaultListView 通过 Breadcrumbs 间接渲染）
const fileViewer = read("components/file/FileViewer.tsx");
assert.match(
  fileViewer,
  /<button[\s\S]*data-onboarding="project-home"[\s\S]*rootBadgeButtonStyle[\s\S]*background: "var\(--node-badge-bg\)"[\s\S]*color: String\(rootColor \|\| ""\)\.trim\(\) \|\|/,
);
assert.doesNotMatch(
  fileViewer,
  /<span[\s\S]*data-onboarding="project-home"[\s\S]*rootBadgeStyle/,
);
assert.match(fileTreeEntries, /fontWeight: isManagedRootNode \? 600 : 400/);
assert.doesNotMatch(fileTree, /fileTree\.onboarding/);
assert.doesNotMatch(fileTree, /fileTree\.showHiddenFiles/);
assert.doesNotMatch(fileTree, /fileTree\.multiProjectSessions/);
assert.doesNotMatch(fileTree, /fileTree\.swapSidebars/);
// 这两个常量现在带显式字面量类型标注：它们由 useAppStateLayer 返回，对象属性位会把
// true 拓宽成 boolean，而下游层与 JSX 组件的签名按原 App 的推断写成 true，拓宽即 TS2322。
// 标注是承重的，所以这里连同标注一起断言（比原来更严，不是放宽）。
assert.match(appStateLayer, /const showHiddenFiles: true = true;/);
assert.match(appStateLayer, /const multiProjectSessionsEnabled: true = true;/);
assert.doesNotMatch(app, /setShowHiddenFiles/);
assert.doesNotMatch(app, /setMultiProjectSessionsEnabled/);
assert.match(sessionPane, /<DefaultListView[\s\S]*rootColor=\{[^}]*(getDisplayNodeColor|\._nodeColor)[^}]*\}/);
assert.match(vite, /defineConfig\(/);
assert.doesNotMatch(vite, /entryFileNames:\s*[^\n]*Date\.now/);
// 右面板分组头的颜色回退链必须走 resolveGroupColor（不再裸 PALETTE[0]/#2563eb 蓝回退）
assert.match(sessionCard, /_nodeColor\s*\|\|\s*resolveGroupColor/);
assert.match(sessionCard, /resolveGroupColor\(\s*\{ rootId[\s\S]*_nodeId/);

// ── 强调色唯一来源 = 节点色；选中底色一律中性灰 ──
// 旧的「默认主题色」变量必须整体退场（含 meadow/moss 的绿色强调色）
for (const dead of ["--accent-hover", "--panel-focus-shadow", "--root-badge-bg", "--root-badge-border", "--root-badge-text"]) {
  assert.doesNotMatch(css, new RegExp(`${dead}\\s*:`), `${dead} should be removed from index.css`);
}
// 5 个主题块（:root / dark media / data-theme=dark / meadow / moss）的强调色统一为 local 节点蓝
const accentValues = [...css.matchAll(/--accent-color:\s*([^;]+);/g)].map((m) => m[1].trim());
assert.equal(accentValues.length, 5, "expected 5 theme blocks to define --accent-color");
assert.ok(accentValues.every((v) => v === "#3b82f6"), `--accent-color must be #3b82f6 in every theme, got ${accentValues.join(", ")}`);
assert.match(nodeRegistry, /DEFAULT_NODE_COLOR = PALETTE\[0\]/);
// 选中底色与节点行选中底色同值：全中性灰，不随主题染蓝/金/绿
assert.equal(
  [...css.matchAll(/--selection-bg:\s*([^;]+);/g)].map((m) => m[1].trim()).join("|"),
  [...css.matchAll(/--node-row-selected-bg:\s*([^;]+);/g)].map((m) => m[1].trim()).join("|"),
  "--selection-bg must match --node-row-selected-bg in every theme",
);
// 不得再有绕开 CSS 变量的选中态硬编码蓝
assert.doesNotMatch(agentSelector, /#3b82f6|rgba\(59, 130, 246/, "AgentSelector must use theme variables, not hardcoded blue");
assert.match(mainViewSwitcher, /var\(--node-row-selected-bg\)/, "main view switcher selected bg must be the neutral node-row token");
assert.doesNotMatch(mainViewSwitcher, /#2563eb/, "main view switcher must not inline the old default accent");
// rootBadgeStyle 只保留中性灰底，色由各消费点的节点色决定
assert.match(rootBadgeStyle, /var\(--node-badge-bg\)/);
assert.doesNotMatch(rootBadgeStyle, /--root-badge-/, "rootBadgeStyle must not reference removed theme badge colors");

// 新建任务面板的「项目」下拉：项目名各用各的节点色，和工作台项目行同一套口径。
// Select 是自绘的（option 是 <button role="option">，不是原生 <option>），所以逐项着色可行。
const selectCmp = read("components/common/Select.tsx");
assert.match(selectCmp, /color\?: string;/, "SelectOption must accept a per-option label color");
assert.match(
  selectCmp,
  /option\.color \? \{ color: option\.color \} : null/,
  "the option label must honor its own color",
);
assert.match(
  selectCmp,
  /color: selectedColor\s*\|\|/,
  "the collapsed trigger must keep the selected option's color",
);
assert.match(
  appProjectDisplay,
  /color: getDisplayNodeColor\(id\) \|\| undefined/,
  "the project dropdown must color each project by its node color",
);
// taskCreateProjectOptions 引用 getDisplayNodeColor，而 const 不提升 —— 声明顺序不能反。
assert.ok(
  appStateLayer.indexOf("taskCreateProjectOptions") > appStateLayer.indexOf("getDisplayNodeColor"),
  "taskCreateProjectOptions must be declared after getDisplayNodeColor (const is not hoisted)",
);
