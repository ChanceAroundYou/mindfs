import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

// 跨节点「正在回复」状态（小蓝灯）刷新：跨节点丢状态 + 切节点全灭的回归。
// 背景：蓝灯真值是 multiProjectPendingByKey（键 = scopeSessionKey(nodeId,root,key)），
// 曾经只打**当前激活节点**，于是「在 PC 上看、任务跑在 Local」时 Local 的灯永远不亮。
// 修法：逐节点拉 + 逐节点增量合并（失败节点与未拉取节点都保留旧值）。
//
// 纯逻辑测试，不依赖浏览器/网络：只加载两个纯函数模块。

function loadModule(relPath, exportNames) {
  const sourcePath = path.resolve(import.meta.dirname, "..", relPath);
  let source = fs.readFileSync(sourcePath, "utf8");
  // 两个模块之间的 require 关系在沙箱里手工接上
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const sandbox = { exports: {}, module: { exports: {} }, require: () => ({}) };
  vm.runInNewContext(compiled, sandbox, { filename: sourcePath });
  for (const name of exportNames) {
    assert.equal(typeof sandbox.exports[name], "function", `${relPath} must export ${name}`);
  }
  return sandbox.exports;
}

const { scopeSessionKey, sessionKeyNodeId } = loadModule("src/services/scope.ts", [
  "scopeSessionKey",
  "sessionKeyNodeId",
]);

// appSession.ts 依赖 ./appTask 与 ../services/session，只取本次用到的那一个纯函数：
// 用桩 module 顶掉那些 require，避免把 React 依赖拖进沙箱。
{
  const sourcePath = path.resolve(import.meta.dirname, "..", "src/app/appSession.ts");
  const source = fs.readFileSync(sourcePath, "utf8");
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const sandbox = {
    exports: {},
    module: { exports: {} },
    require: (id) => {
      if (id.includes("scope")) return { sessionKeyNodeId };
      if (id.includes("appTask")) return { normalizeFastService: (v) => v };
      return {};
    },
  };
  vm.runInNewContext(compiled, sandbox, { filename: sourcePath });
  globalThis.__mergeReplyingStateByNode = sandbox.exports.mergeReplyingStateByNode;
}
const mergeReplyingStateByNode = globalThis.__mergeReplyingStateByNode;
assert.equal(typeof mergeReplyingStateByNode, "function", "appSession must export mergeReplyingStateByNode");

// ── 1. 节点作用域：同名项目在两个节点上的会话键必须互不相同 ───────────────
const pcKey = scopeSessionKey("pc", "go", "s1");
const homeKey = scopeSessionKey("home", "go", "s1");
assert.notEqual(pcKey, homeKey, "同名项目跨节点的会话键必须区分");
assert.equal(pcKey, "pc::go::s1");
assert.equal(homeKey, "home::go::s1");

// 反解回节点：合并时据此判断「这个键属于谁」
assert.equal(sessionKeyNodeId(pcKey), "pc");
assert.equal(sessionKeyNodeId(homeKey), "home");
// 历史单节点格式（空 nodeId）反解为空 —— 归属不明，必须保留而不是丢弃
assert.equal(sessionKeyNodeId("go::s1"), "");

// ── 2. 失败保留：PC 拉到、home 挂掉时，home 的在跑会话灯不能灭 ──────────────
const previous = { [pcKey]: true, [homeKey]: true };
// 本轮 home 请求失败 → okNodeIds 只有 pc
const merged1 = mergeReplyingStateByNode(previous, { [pcKey]: true }, ["pc"]);
assert.equal(merged1[homeKey], true, "失败的节点必须保留上一轮的在跑状态，否则灯被误灭");
assert.equal(merged1[pcKey], true);

// 成功拉到但这次没有在跑 → 该节点自己的键被清掉（这才是「灯灭」的正确路径）
const merged2 = mergeReplyingStateByNode(previous, {}, ["pc", "home"]);
assert.ok(!(pcKey in merged2), "成功拉到且无在跑会话 → 键被清除");
assert.ok(!(homeKey in merged2), "成功拉到且无在跑会话 → 键被清除");

// ── 3. 切节点回归：只有新激活节点被重算，另一节点的在跑会话原样保留 ────────
// 场景：在 PC 上看，home 上有任务在跑；切到 home → 刷新只拿到 home 的响应。
const beforeSwitch = { [homeKey]: true };
const afterSwitch = mergeReplyingStateByNode(beforeSwitch, { [homeKey]: true }, ["home"]);
assert.equal(
  afterSwitch[homeKey],
  true,
  "切节点时 home 上在跑的会话灯不能灭（旧实现整体替换会当场全灭）",
);

// ── 4. 去重：两节点各自返回同名 root 的不同 session，两边都在 ────────────
const twoNodes = mergeReplyingStateByNode(
  {},
  { [pcKey]: true, [homeKey]: true },
  ["pc", "home"],
);
assert.equal(Object.keys(twoNodes).length, 2, "两节点的条目必须都在，不能互相覆盖");

// ── 5. 空 nodeId 的历史键：归属不明，一律保留 ─────────────────────────────
const legacyMerged = mergeReplyingStateByNode({ "go::s1": true }, { [pcKey]: true }, ["pc"]);
assert.equal(legacyMerged["go::s1"], true, "无法反推归属的历史键必须保留");

// ── 6. 全部节点失败：okNodeIds 为空 → 调用方应当整轮放弃、不写 state ──────
// （mergeReplyingStateByNode 本身保持幂等：空 fresh + 空 ok 会原样返回）
const allFailed = mergeReplyingStateByNode(previous, {}, []);
assert.deepEqual(
  Object.keys(allFailed).sort(),
  Object.keys(previous).sort(),
  "全部失败时不得丢失任何已有状态",
);

console.log("cross-node-replying-state.test.mjs ok");
