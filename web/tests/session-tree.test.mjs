import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

const sourcePath = path.resolve(import.meta.dirname, "../src/services/sessionTree.ts");
const source = fs.readFileSync(sourcePath, "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.CommonJS,
    target: ts.ScriptTarget.ES2020,
  },
}).outputText;

const sandbox = {
  exports: {},
  module: { exports: {} },
};
vm.runInNewContext(compiled, sandbox, { filename: sourcePath });

const { collectSessionSubtreeKeys, pruneChildState } = sandbox.exports;

/**
 * vm 沙箱里造的 Array 与宿主 realm 的原型不同，`assert.deepEqual`（strict 版）
 * 会判成不相等。统一过 JSON 比结构，避开跨 realm 的原型差异。
 */
const plain = (value) => JSON.parse(JSON.stringify(value));
const keysOf = (items, rootKey) => plain(collectSessionSubtreeKeys(items, rootKey)).sort();

/**
 * 2026-09：删除父会话后子会话没被摘掉，会被树构建判成「父不在集合里」而提升成
 * 顶层整宽行 —— 面板撑爆且收不回去。这组用例守住根因修复。
 */

test("collectSessionSubtreeKeys 收集父会话与全部后代", () => {
  const items = [
    { key: "p" },
    { key: "c1", parent_session_key: "p" },
    { key: "c2", parent_session_key: "p" },
    { key: "g1", parent_session_key: "c1" },
    { key: "g2", parent_session_key: "g1" },
    { key: "unrelated" },
  ];
  assert.deepEqual(keysOf(items, "p"), ["c1", "c2", "g1", "g2", "p"]);
});

test("collectSessionSubtreeKeys 跨项目来源的子会话一并收进来", () => {
  // 回归用例：只遍历 sessionsRef 时分组里的 c1/g1 收不到，
  // 它们会被提升成顶层行 —— 面板爆长的直接原因。
  const currentProject = [{ key: "p" }];
  const groups = [
    { key: "c1", parent_session_key: "p" },
    { key: "g1", parent_session_key: "c1" },
  ];
  assert.deepEqual(keysOf([...currentProject, ...groups], "p"), ["c1", "g1", "p"]);
  // 对照：只给当前项目列表时确实漏（证明上面那条修复是必要的）
  assert.deepEqual(keysOf(currentProject, "p"), ["p"]);
});

test("collectSessionSubtreeKeys 不收无关会话", () => {
  const items = [
    { key: "p" },
    { key: "c1", parent_session_key: "p" },
    { key: "other" },
    { key: "orphan", parent_session_key: "missing" },
  ];
  assert.deepEqual(keysOf(items, "p"), ["c1", "p"]);
});

test("collectSessionSubtreeKeys 对环形父子关系不死循环", () => {
  const items = [
    { key: "a", parent_session_key: "b" },
    { key: "b", parent_session_key: "a" },
  ];
  assert.deepEqual(keysOf(items, "a"), ["a", "b"]);
});

test("collectSessionSubtreeKeys 空 rootKey 返回空", () => {
  assert.deepEqual(plain(collectSessionSubtreeKeys([{ key: "p" }], "")), []);
  assert.deepEqual(plain(collectSessionSubtreeKeys([], "")), []);
});

test("collectSessionSubtreeKeys 本地状态为空时仍返回被删的根 key", () => {
  // 根会话一定存在（我们刚删掉它），它自身必须进集合，否则面板上那一行不会被摘掉。
  assert.deepEqual(plain(collectSessionSubtreeKeys([], "p")), ["p"]);
});

test("collectSessionSubtreeKeys 同时支持 session_key 字段", () => {
  const items = [
    { session_key: "p" },
    { session_key: "c1", parent_session_key: "p" },
  ];
  assert.deepEqual(keysOf(items, "p"), ["c1", "p"]);
});

test("pruneChildState 丢掉已删除会话的展开态", () => {
  const state = {
    "n1::rootA:p": true,
    "n1::rootA:c1": true,
    "n1::rootA:gone": true,
  };
  const live = new Set(["p", "c1"]);
  assert.deepEqual(plain(pruneChildState(state, live)), {
    "n1::rootA:p": true,
    "n1::rootA:c1": true,
  });
});

test("pruneChildState 不误伤其他 root/node 下同名的 key", () => {
  // stateKey 前缀里同时有 `::` 和 `:`，按最后一个 `:` 切才拿得到真正的会话 key。
  const state = {
    "n1::rootA:keep": true,
    "n2::rootB:keep": true,
  };
  const live = new Set(["keep"]);
  assert.deepEqual(plain(pruneChildState(state, live)), plain(state));
});

test("pruneChildState 会话全部消失时清空", () => {
  assert.deepEqual(plain(pruneChildState({ "n1::rootA:p": true }, new Set())), {});
});
