import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

test("project-tree-tab-and-root-toggle", async () => {
// 两个项目文件树交互 bug（2026-10-20 用户实测）：
//   B. 打开左侧文件树侧边栏总是跳到 git 页 —— 要默认「文件」页。
//   C. 跨节点点项目名前的下拉箭头收起项目树没反应，必须先点进项目才行 ——
//      箭头要**纯展开/收起**（跨节点也一样），点项目名才是「进入项目」。

function loadModule(relPath, exportNames, requireStub = () => ({})) {
  const sourcePath = path.resolve(import.meta.dirname, "..", relPath);
  const source = fs.readFileSync(sourcePath, "utf8");
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const sandbox = { exports: {}, module: { exports: {} }, require: requireStub, URLSearchParams };
  vm.runInNewContext(compiled, sandbox, { filename: sourcePath });
  for (const name of exportNames) {
    assert.equal(typeof sandbox.exports[name], "function", `${relPath} must export ${name}`);
  }
  return sandbox.exports;
}

const scope = loadModule("src/shared/scope.ts", ["expandKey", "treeKey"]);

// ── C. 根行箭头 = 纯展开/收起，不进项目 ──────────────────────────────
const apiCalls = [];
const { createOpenDirectoryAction } = loadModule(
  "src/app/openDirectoryAction.ts",
  ["createOpenDirectoryAction"],
  (id) => {
    if (id.includes("shared/scope")) return scope;
    if (id.includes("services/net/api")) {
      return {
        protectedJSON: async (url) => {
          apiCalls.push(url);
          return { entries: [{ name: "a.txt", path: "a.txt", is_dir: false }] };
        },
        ProtectedAPIError: class ProtectedAPIError extends Error {},
      };
    }
    if (id.includes("services/net/base")) return { appURL: (p, q, nodeId) => `${p}?${q}${nodeId ? `&node=${nodeId}` : ""}` };
    if (id.includes("appUtils")) return { normalizeTreeResponse: (p) => ({ entries: p?.entries || [] }) };
    if (id.includes("services/net/error")) return { reportError: () => {} };
    return {};
  },
);

function makeParams(overrides = {}) {
  const calls = {
    selectRootNode: [],
    setCurrentRootId: [],
    setExpanded: [],
    setEntriesByPath: [],
    api: apiCalls,
  };
  const params = {
    fileOpenRequestRef: { current: 0 },
    currentRootIdRef: { current: "other" },
    currentRootNodeIdRef: { current: "home" },
    entriesByPathRef: { current: {} },
    expandedRef: { current: [] },
    invalidTreeCacheKeysRef: { current: new Set() },
    setGitDiff: () => {},
    setCurrentRootId: (v) => calls.setCurrentRootId.push(v),
    setEntriesByPath: (fn) => calls.setEntriesByPath.push(fn),
    setExpanded: (fn) => calls.setExpanded.push(fn),
    selectRootNode: (root, nodeId) => calls.selectRootNode.push([root, nodeId]),
    getNodeIdForRoot: () => "",
    ...overrides,
  };
  return { params, calls };
}

// 跨节点：项目在 pc 节点，浏览器停在 home 节点。
{
  const { params, calls } = makeParams();
  const action = createOpenDirectoryAction(params, false, (k) => k);
  await action({ path: "proj", root: "proj", isRoot: true, toggle: true, nodeId: "pc" });

  assert.deepEqual(calls.selectRootNode, [], "点箭头不能进入项目（跨节点也不行）");
  assert.deepEqual(calls.setCurrentRootId, [], "点箭头不能改当前项目");
  const expanded = calls.setExpanded.length === 1 ? calls.setExpanded[0]([]) : null;
  assert.ok(expanded, "点箭头必须改展开状态");
  assert.ok(
    expanded.includes(scope.expandKey("pc", "proj", "proj", true)),
    `展开键必须是该节点该项目的根键：${JSON.stringify(expanded)}`,
  );
  // 顶层目录要按**该根所属的节点**去取，不能打当前节点
  assert.ok(
    apiCalls.some((u) => String(u).includes("node=pc")),
    `顶层目录必须按所属节点取：${JSON.stringify(apiCalls)}`,
  );
}

// 已展开时再点箭头 = 收起（且不再拉数据）
{
  apiCalls.length = 0;
  const { params, calls } = makeParams({
    expandedRef: { current: [scope.expandKey("pc", "proj", "proj", true)] },
  });
  const action = createOpenDirectoryAction(params, false, (k) => k);
  await action({ path: "proj", root: "proj", isRoot: true, toggle: true, nodeId: "pc" });

  assert.deepEqual(calls.selectRootNode, []);
  const expanded = calls.setExpanded.length === 1 ? calls.setExpanded[0]([scope.expandKey("pc", "proj", "proj", true)]) : null;
  assert.ok(expanded && !expanded.includes(scope.expandKey("pc", "proj", "proj", true)), "再点一次必须收起");
  assert.deepEqual(apiCalls, [], "收起不需要拉数据");
}

// ── B. 默认打开「文件」页 ────────────────────────────────────────────
//
// 真相在 App 层（useAppFileWorkspaceState 的 useState 默认值）；FileTree 只能
// 受控。此前 FileTree 自己读 localStorage 初始化，而那个键**从来没人写** ——
// 于是侧边栏永远停在一个陈旧的 "git" 上。
const workspaceSrc = fs.readFileSync(
  path.resolve(import.meta.dirname, "..", "src/app/useAppFileWorkspaceState.ts"),
  "utf8",
);
assert.match(
  workspaceSrc,
  /useState<ProjectTreeTab>\("files"\)/,
  "项目树页签的默认必须是 files（不是 git）",
);

const fileTreeSrc = fs.readFileSync(
  path.resolve(import.meta.dirname, "..", "src/components/file/FileTree.tsx"),
  "utf8",
);
assert.match(fileTreeSrc, /projectTreeTab = "files"/, "FileTree 的受控默认值必须是 files");
assert.doesNotMatch(
  fileTreeSrc,
  /localStorage\.getItem\([^)]*[Tt]ab/,
  "FileTree 不得再自己从 localStorage 读页签（那个键从来没人写）",
);

const treeTypesSrc = fs.readFileSync(
  path.resolve(import.meta.dirname, "..", "src/components/file/fileTreeTypes.ts"),
  "utf8",
);
assert.doesNotMatch(
  treeTypesSrc,
  /PROJECT_TREE_TAB_STORAGE_KEY/,
  "死掉的 localStorage 键必须删掉（留着就会有人以为它生效）",
);
assert.match(treeTypesSrc, /projectTreeTab\?: ProjectTreeTab;/);
assert.match(treeTypesSrc, /onProjectTreeTabChange\?: \(tab: ProjectTreeTab\) => void;/);

const panelSrc = fs.readFileSync(
  path.resolve(import.meta.dirname, "..", "src/components/app/FileTreePanel.tsx"),
  "utf8",
);
assert.match(panelSrc, /projectTreeTab=\{projectTreeTab\}/, "FileTreePanel 必须把页签传下去");

const rootViewSrc = fs.readFileSync(
  path.resolve(import.meta.dirname, "..", "src/app/AppRootView.tsx"),
  "utf8",
);
assert.match(rootViewSrc, /projectTreeTab=\{projectTreeTab\}/, "AppRootView 必须把页签传下去");

console.log("project-tree-tab-and-root-toggle.test.mjs: OK");
});
