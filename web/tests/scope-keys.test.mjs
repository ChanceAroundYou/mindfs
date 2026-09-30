import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

// scope.ts 键格式合约：空节点 → 历史单节点格式（字节兼容）；非空节点 → 复合键；
// 2 段/3 段键互不相等（无碰撞）；目录键与会话键分隔符不同。
const root = path.resolve(import.meta.dirname, "..");
const sourcePath = path.join(root, "src/services/scope.ts");
const source = fs.readFileSync(sourcePath, "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText;
const sandbox = { exports: {}, module: { exports: {} } };
vm.runInNewContext(compiled, sandbox, { filename: sourcePath });

const { scopeKey, scopeSessionKey, treeKey, expandKey, dirSelKey, sessionScope } =
  sandbox.exports;

// 单节点（空 nodeId）== 历史格式，逐字节兼容
assert.equal(scopeKey("", "mindfs"), "mindfs");
assert.equal(scopeKey(null, "mindfs"), "mindfs");
assert.equal(scopeKey(undefined, "mindfs"), "mindfs");
assert.equal(scopeSessionKey("", "mindfs", "abc"), "mindfs::abc");
assert.equal(treeKey("", "mindfs", "."), "mindfs");
assert.equal(treeKey("", "mindfs", "src"), "mindfs:src");
assert.equal(expandKey("", "mindfs", "mindfs", true), "mindfs");
assert.equal(expandKey("", "mindfs", "src", false), "mindfs:src");
assert.equal(dirSelKey("", "mindfs", "mindfs", true), "mindfs");

// 多节点复合键
assert.equal(scopeKey("home", "mindfs"), "home::mindfs");
assert.equal(scopeKey("pc", "mindfs"), "pc::mindfs");
assert.equal(scopeSessionKey("home", "mindfs", "abc"), "home::mindfs::abc");
assert.equal(treeKey("home", "mindfs", "."), "home::mindfs");
assert.equal(treeKey("home", "mindfs", "src"), "home::mindfs:src");
assert.equal(expandKey("home", "mindfs", "mindfs", true), "home::mindfs");
assert.equal(expandKey("home", "mindfs", "src", false), "home::mindfs:src");
assert.equal(sessionScope("home", "mindfs", "abc"), "home::mindfs::abc");

// 无碰撞：2 段旧键与任何 3 段新键互不相等；目录键 vs 会话键互不相同
const legacyKeys = [
  scopeKey("", "mindfs"),
  scopeSessionKey("", "mindfs", "abc"),
  treeKey("", "mindfs", "src"),
];
const scopedKeys = [
  scopeKey("home", "mindfs"),
  scopeSessionKey("home", "mindfs", "abc"),
  treeKey("home", "mindfs", "src"),
];
for (const legacy of legacyKeys) {
  for (const scoped of scopedKeys) {
    assert.notEqual(legacy, scoped, `${legacy} must never equal ${scoped}`);
  }
}
assert.notEqual(scopeSessionKey("home", "mindfs", "abc"), treeKey("home", "mindfs", "abc"));
assert.notEqual(scopeSessionKey("", "mindfs", "abc"), treeKey("", "mindfs", "abc"));

// 段数与来源一致性：3 段键只在 nodeId 非空时产生
assert.equal(scopeSessionKey("home", "mindfs", "abc").split("::").length, 3);
assert.equal(scopeSessionKey("", "mindfs", "abc").split("::").length, 2);
assert.ok(!scopeKey("", "mindfs").includes("::"));
assert.ok(scopeKey("home", "mindfs").includes("::"));

console.log("scope-keys.test.mjs ok");
