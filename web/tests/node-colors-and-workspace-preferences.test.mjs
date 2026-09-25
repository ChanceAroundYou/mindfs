import assert from "node:assert/strict";
import fs from "node:fs";

const read = (file) => fs.readFileSync(new URL(`../src/${file}`, import.meta.url), "utf8");
const nodeRegistry = read("services/nodeRegistry.ts");
const defaultList = read("components/DefaultListView.tsx");
const fileTree = read("components/FileTree.tsx");
const sessionList = read("components/SessionList.tsx");
const app = read("App.tsx");
const vite = fs.readFileSync(new URL("../vite.config.ts", import.meta.url), "utf8");
const css = read("index.css");
const agentSelector = read("components/AgentSelector.tsx");
const mainViewSwitcher = read("components/MainViewSwitcher.tsx");
const rootBadgeStyle = read("components/rootBadgeStyle.ts");

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
assert.match(
  defaultList,
  /<button[\s\S]*data-onboarding="project-home"[\s\S]*rootBadgeButtonStyle[\s\S]*background: "var\(--node-badge-bg\)"[\s\S]*color: String\(rootColor \|\| ""\)\.trim\(\) \|\|/,
);
assert.doesNotMatch(
  defaultList,
  /<span[\s\S]*data-onboarding="project-home"[\s\S]*rootBadgeStyle/,
);
assert.match(fileTree, /fontWeight: isManagedRootNode \? 600 : 400/);
assert.doesNotMatch(fileTree, /fileTree\.onboarding/);
assert.doesNotMatch(fileTree, /fileTree\.showHiddenFiles/);
assert.doesNotMatch(fileTree, /fileTree\.multiProjectSessions/);
assert.doesNotMatch(fileTree, /fileTree\.swapSidebars/);
assert.match(app, /const showHiddenFiles = true;/);
assert.match(app, /const multiProjectSessionsEnabled = true;/);
assert.doesNotMatch(app, /setShowHiddenFiles/);
assert.doesNotMatch(app, /setMultiProjectSessionsEnabled/);
assert.match(app, /<DefaultListView[\s\S]*rootColor=\{[^}]*(getDisplayNodeColor|\._nodeColor)[^}]*\}/);
assert.match(vite, /defineConfig\(/);
assert.doesNotMatch(vite, /entryFileNames:\s*[^\n]*Date\.now/);
// 右面板分组头的颜色回退链必须走 resolveGroupColor（不再裸 PALETTE[0]/#2563eb 蓝回退）
assert.match(sessionList, /_nodeColor \|\| resolveGroupColor/);
assert.match(sessionList, /resolveGroupColor\(\s*\{ rootId[\s\S]*_nodeId/);

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
