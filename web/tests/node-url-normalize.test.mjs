import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";

const root = path.resolve(import.meta.dirname, "..");
const sourcePath = path.join(root, "src/services/nodeBase.ts");
const source = fs.readFileSync(sourcePath, "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText;
const sandbox = { exports: {}, module: { exports: {} }, URL };
vm.runInNewContext(compiled, sandbox, { filename: sourcePath });

const {
  deriveLocalNodeBase,
  normalizeExplicitNodeBase,
  repairDuplicateDeployPrefix,
} = sandbox.exports;

assert.equal(normalizeExplicitNodeBase(" https://pc.example/ "), "https://pc.example");
assert.equal(normalizeExplicitNodeBase("https://pc.example/mindfs/"), "https://pc.example/mindfs");
assert.equal(deriveLocalNodeBase("https://home.example", "/mindfs"), "https://home.example/mindfs");
assert.equal(deriveLocalNodeBase("https://home.example", ""), "https://home.example");
assert.equal(repairDuplicateDeployPrefix("https://pc.example/mindfs/mindfs", "/mindfs"), "https://pc.example/mindfs");
assert.equal(repairDuplicateDeployPrefix("https://pc.example/mindfs/mindfs/api", "/mindfs"), "https://pc.example/mindfs/api");
assert.equal(repairDuplicateDeployPrefix("https://pc.example/mindfs/api/mindfs", "/mindfs"), "https://pc.example/mindfs/api/mindfs");
assert.equal(repairDuplicateDeployPrefix("https://pc.example/other", "/mindfs"), "https://pc.example/other");

const fileTree = fs.readFileSync(path.join(root, "src/components/FileTree.tsx"), "utf8");
const registry = fs.readFileSync(path.join(root, "src/services/nodeRegistry.ts"), "utf8");
assert.match(fileTree, /const testUrl = normalized;/, "node probe must use the explicit remote base");
assert.doesNotMatch(fileTree, /normalizeBaseURLWithPrefix\(normalized\)/, "node probe must not inherit the local prefix");
assert.match(registry, /const cacheAtStart = cache;/, "initial sync must detect a concurrent local write");
assert.match(registry, /if \(cache !== cacheAtStart\) return cache \|\| nodes;/, "initial sync must not overwrite a concurrent local write");
assert.match(registry, /await enqueueServerWrite\(merged\);\s*nodes = merged;\s*clearLegacyLocalNodes\(\);/s, "legacy nodes must only be cleared after a successful server write");
assert.doesNotMatch(registry, /if \(syncing\) return/, "overlapping node writes must not be dropped");
assert.match(registry, /const write = writeQueue\.then\(\(\) => pushToServer\(snapshot\)\);/, "node writes must be serialized");

console.log("node-url-normalize.test.mjs: OK");
