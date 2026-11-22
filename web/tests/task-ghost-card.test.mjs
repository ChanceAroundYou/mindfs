import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

test("task-ghost-card", async () => {
// 幽灵卡：服务端已经删掉、客户端还渲染着的那张卡。
//
// 实测（2026-11-20）：llmux #16（task_6c2b5e2430af72eb）10:24:36 删除成功
// （HTTP 200），12:29 浏览器里还画着这张卡。点「完成」回 400
// {"error":"sql: no rows in result set"}，点「删除」回 404 同一句话 ——
// 用户看到的就是「既不能完成也不能删除」。
//
// 两个叠加的成因，各修一处：
//   1) 首次加载走的是增量同步（after=updatedAt 水位）。增量响应在结构上就
//      看不见「少了谁」，于是缓存里的幽灵卡每次刷新都活着。
//   2) 任务级动作拿到 no rows 只弹一条红字，卡片原地不动，点一次失败一次。
//
// 判据：任务级请求回 no rows ⇒ 判为「这张卡在服务端不存在了」。

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

// —— isTaskGoneError：行为测试（纯函数，沙箱里直接跑） ——
function loadGoneModule() {
  const sourcePath = path.resolve(root, "src/services/task/gone.ts");
  const compiled = ts.transpileModule(fs.readFileSync(sourcePath, "utf8"), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const sandbox = { exports: {}, module: { exports: {} }, require: () => ({}) };
  sandbox.module.exports = sandbox.exports;
  vm.runInNewContext(compiled, sandbox, { filename: sourcePath });
  return sandbox.exports;
}

const { isTaskGoneError } = loadGoneModule();
// APIError 的构造形状见 src/services/net/api.ts：name = "APIError"，message 取
// payload.message || payload.error。
const apiError = (status, message) => Object.assign(new Error(message), { name: "APIError", status });

assert.equal(isTaskGoneError(apiError(400, "sql: no rows in result set")), true, "move 系列回 400，正文是 ErrNoRows");
assert.equal(isTaskGoneError(apiError(404, "sql: no rows in result set")), true, "delete 回 404，同一句话");
assert.equal(isTaskGoneError(apiError(400, "root_id and id required")), false, "参数错误不是幽灵卡");
assert.equal(isTaskGoneError(apiError(404, "root not found")), false, "root 找不到不能当成任务被删");
assert.equal(isTaskGoneError(new Error("sql: no rows in result set")), false, "非 API 错误不参与判定");
assert.equal(isTaskGoneError(null), false);

// —— 首次加载必须做一次全量对账 ——
const board = read("src/app/useTaskBoard.ts");
assert.match(
  board,
  /const authoritative = force \|\| !reconciledRootsRef\.current\.has\(scopeKey\)/,
  "本页第一次拉某个 root 必须走全量：增量看不见删除，幽灵卡会一直活到手动刷新",
);
assert.match(
  board,
  /taskSyncFilters\(meta, authoritative\)/,
  "增量水位必须跟着 authoritative 走，否则全量对账时仍会带上 after",
);
assert.match(
  board,
  /if \(authoritative\) \{\s*\n\s*const keepTaskIds/,
  "只有权威响应才能拿去淘汰缓存（force 之外还包括首次加载）",
);
// 对账成功才记账：请求失败 / 被新请求 abort 时下一次仍须全量重试。
assert.match(
  board,
  /reconciledRootsRef\.current\.add\(scopeKey\)/,
  "对账成功才写入已对账集合",
);

// —— 动作失败时的收尾：摘卡 + 说明 ——
assert.match(
  board,
  /if \(isTaskGoneError\(err\)\) \{\s*\n\s*dropKanbanTask\(/,
  "看板动作拿到 no rows 必须把那张卡摘掉，否则点一次失败一次",
);
assert.match(
  board,
  /const dropKanbanTask = useCallback\(\(rootId: string, taskId: string, nodeId\?: string\) => \{[\s\S]*?pruneTaskDetails\(rootId, keep, nodeId\);[\s\S]*?pruneCachedTaskDetails\(rootId, keep, nid\);/,
  "摘卡要同时清内存与 IndexedDB —— 只清一边，下次加载又会被缓存喂回来",
);
assert.match(
  board,
  /t\("task\.goneRemoved"\)/,
  "摘卡要给出可读的原因，而不是把 sql: no rows in result set 甩给用户",
);

// —— 详情面板走同一条收尾路径 ——
const panel = read("src/components/task/useTaskDetailPanel.ts");
assert.match(
  panel,
  /if \(isTaskGoneError\(err\)\) \{\s*\n\s*onTaskGone\?\.\(task\.root_id \|\| "", task\.id\);/,
  "详情面板的改名/完成/删除/暂停都要识别幽灵卡",
);
assert.match(
  panel,
  /onTaskGone\?\.\([\s\S]*?reportError\("file\.write_failed", t\("task\.goneRemoved"\)[\s\S]*?onClose\(\);/,
  "识别到幽灵卡后要摘卡、说清楚、关面板",
);
// 八个任务级动作的 catch 必须都经过 fail()，不能各写一份 reportError ——
// 否则新加的动作会悄悄漏掉幽灵卡处理（取消/归档 2026-11-22 加入详情，同日又加了「开始」）。
assert.equal(
  (panel.match(/catch \(error\) \{ fail\(error\); \}/g) || []).length,
  8,
  "finishWorktree / complete / pause-resume / reopen / delete / cancel / archive / start 八个 catch 统一走 fail()",
);

// —— 接线：面板 → 看板 ——
const gate = read("src/components/app/TaskDetailPanelGate.tsx");
assert.match(gate, /onTaskGone=\{\(rootId, taskId\) => dropKanbanTask\(/, "gate 要把幽灵卡交给看板摘");
const rootView = read("src/app/AppRootView.tsx");
assert.match(rootView, /dropKanbanTask=\{dropKanbanTask\}/, "AppRootView 要把 dropKanbanTask 传给 gate");
const stateLayer = read("src/app/useAppStateLayer.ts");
assert.match(stateLayer, /dropKanbanTask,/, "state 层要透出 dropKanbanTask");
});
