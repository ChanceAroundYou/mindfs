import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

test("fork-session-name", async () => {
const source = fs.readFileSync("src/components/SessionList.tsx", "utf8");
const ast = ts.createSourceFile("SessionList.tsx", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
const declaration = ast.statements.find(
  (node) => ts.isFunctionDeclaration(node) && node.name?.text === "forkSessionDisplayName",
);
assert.ok(declaration);
// 拆分后这个纯函数被搬到 sessionListShared.tsx 并加了 `export`（SessionCard 也要用）。
// 这里只关心函数体，所以剥掉导出修饰符再丢进 vm —— 否则 vm 会因裸 `export` 报语法错。
const compiled = ts.transpileModule(declaration.getText(ast).replace(/^export\s+/, ""), {
  compilerOptions: { target: ts.ScriptTarget.ES2020 },
}).outputText;
const sandbox = {};
vm.runInNewContext(compiled, sandbox);
const name = sandbox.forkSessionDisplayName;
const parent = new Map([["root:parent", { name: "父会话" }]]);
const fork = { sessionKey: "parent", seq: 4 };

assert.equal(name("自定义子会话名称", fork, parent, "root"), "自定义子会话名称");
assert.equal(name("原父会话#4", fork, parent, "root"), "原父会话#4");
assert.equal(name("", fork, parent, "root"), "父会话#4");
assert.equal(name("", fork, new Map(), "root"), "Fork#4");
assert.equal(name("普通会话", null, parent, "root"), "普通会话");
console.log("fork session names passed");
});
