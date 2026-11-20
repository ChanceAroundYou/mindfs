import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

test("workspace-duplicate-root-routing", async () => {
// 同名项目分布在多台机器上时，工作台卡片动作必须发到**卡片自己那台**。
//
// 实测（2026-11-20）：fn 与 pc 各有一个 id 为 `docs` 的项目（显示名「AIS文档」）。
// 工作台扇出会同时拉两台，但 managedRootById 每个 rootId 只保留首个（pc），
// 于是 fn 的 #1 `docs`（task_b44cc59c6a5f38d0）点「完成 / 删除」全打到 pc ——
// pc 的 docs 库里一条任务都没有，服务端回 `sql: no rows in result set`。
// 用户看到的就是「既不能完成也不能删除」+「它还在工作台里」：卡片是真实存在的，
// 只是动作永远走错机器。

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

// —— taskActionNodeId：行为测试（纯函数，沙箱里直接跑） ——
function loadRoutingModule() {
  const sourcePath = path.resolve(root, "src/services/task/routing.ts");
  const compiled = ts.transpileModule(fs.readFileSync(sourcePath, "utf8"), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const sandbox = { exports: {}, module: { exports: {} }, require: () => ({}) };
  sandbox.module.exports = sandbox.exports;
  vm.runInNewContext(compiled, sandbox, { filename: sourcePath });
  return sandbox.exports;
}

const { taskActionNodeId } = loadRoutingModule();
// byId 里的 `docs` 是 pc 那份（本地没有 docs，pc 先于 fn 进入并集）
const getNodeIdForRoot = (rid) => (rid === "docs" ? "pc" : rid === "mindfs" ? "local" : undefined);

assert.equal(
  taskActionNodeId({ _nodeId: "fn" }, "docs", getNodeIdForRoot),
  "fn",
  "任务自带 _nodeId 时必须压过按 rootId 反查 —— 否则 fn 的卡会被派给 pc",
);
assert.equal(
  taskActionNodeId({ _nodeId: "  " }, "docs", getNodeIdForRoot),
  "pc",
  "空白的 _nodeId 不算数，退回反查",
);
assert.equal(
  taskActionNodeId({}, "docs", getNodeIdForRoot),
  "pc",
  "没有 _nodeId 时保持旧行为：按 rootId 反查",
);
assert.equal(taskActionNodeId(null, "unknown-root", getNodeIdForRoot), undefined, "查不到就是 undefined");
assert.equal(taskActionNodeId({}, "", () => "pc"), undefined, "没有 rootId 不猜节点");

// —— 工作台扇出必须给任务打 _nodeId ——
const workspace = read("src/app/useWorkspaceBoard.ts");
assert.match(
  workspace,
  /task:\s*nid\s*\?\s*\{\s*\.\.\.item\.task,\s*_nodeId:/,
  "扇出拉回的每个 item 都要在自己的 task 上打 _nodeId —— 动作层只认这一处",
);

// —— 就地更新（taskDetailsById 覆盖）不能把 _nodeId 弄丢 ——
assert.match(
  workspace,
  /const nodeId = String\(\(live as any\)\?\._nodeId \|\| ""\)\.trim\(\)/,
  "liveVersion 换上的那份来自 taskDetailsById，未必带 _nodeId，必须补回来",
);
assert.match(
  workspace,
  /task: nodeId \? \{ \.\.\.live, _nodeId: nodeId \} : live/,
  "补标签的写法要显式，别指望 live 自带上",
);

// —— 动作层真的用了它 ——
const board = read("src/app/useTaskBoard.ts");
assert.match(
  board,
  /const nodeId = taskActionNodeId\(task, rootId, getNodeIdForRoot\);/,
  "handleMoveKanbanTask 必须走 taskActionNodeId，不能退回 getNodeIdForRoot(rootId)",
);

// —— 打开项目同样要带分组上的节点 ——
const view = read("src/components/task/TaskBoardView.tsx");
assert.match(
  view,
  /onOpenProject=\{\(rootId, nodeId\) => \{ void openWorkspaceProject\(rootId, nodeId\); \}\}/,
  "点项目分组时丢掉 nodeId 会打开另一台机器上的同名项目（空的）",
);
const actions = read("src/app/useAppWorkspaceActions.ts");
assert.match(
  actions,
  /const targetNodeId = String\(nodeId \|\| ""\)\.trim\(\) \|\| getNodeIdForRoot\(rootId\) \|\| undefined;/,
  "openWorkspaceProject 应该优先用调用方带来的 nodeId",
);
});
