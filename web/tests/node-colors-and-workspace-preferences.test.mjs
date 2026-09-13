import assert from "node:assert/strict";
import fs from "node:fs";

const read = (file) => fs.readFileSync(new URL(`../src/${file}`, import.meta.url), "utf8");
const nodeRegistry = read("services/nodeRegistry.ts");
const defaultList = read("components/DefaultListView.tsx");
const fileTree = read("components/FileTree.tsx");
const sessionList = read("components/SessionList.tsx");
const app = read("App.tsx");
const vite = fs.readFileSync(new URL("../vite.config.ts", import.meta.url), "utf8");

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
