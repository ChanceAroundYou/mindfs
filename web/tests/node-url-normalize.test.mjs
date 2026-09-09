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

// "local" 节点 URL 必须按当前设备推导：共享列表里持久化的 local URL 是某台设备写入的，
// 对其它设备必然错误（实测手机写入 local→home URL，PC 端解析 nodeId="local" 全部串到 home）。
assert.match(registry, /function deviceLocalNodeURL\(\): string \{\s*const base = localBaseURL\(\);\s*if \(!base \|\| !\/\^https\?:\\\/\\\/\/i\.test\(base\)\) return "";\s*return base;\s*\}/, "device-local URL must only accept fetchable http(s) origins");
assert.match(registry, /function makeLocalNode\(\): NodeConnection \{\s*return \{ id: LOCAL_NODE_ID, name: "local", url: deviceLocalNodeURL\(\), color: PALETTE\[0\] \};\s*\}/, "ephemeral local node must use the device-derived URL");
assert.match(registry, /\/\/ 读取时强制按当前设备推导[^\n]*\n[^\n]*if \(local\) local\.url = deviceLocalNodeURL\(\);/, "reading the node list must override the persisted local URL per device");

// 显式 nodeId 解析失败不得静默回退 active node：会把请求发给错误节点。
const runtimeSource = fs.readFileSync(path.join(root, "src/services/runtime.ts"), "utf8");
assert.match(runtimeSource, /function resolveNodeBaseURL\(nodeId\?: string\): string \{\s*if \(nodeId\) \{\s*const node = getNodeById\(nodeId\);\s*if \(node\?\.url\) return normalizeExplicitNodeBase\(node\.url\);[\s\S]*?console\.warn\("\[node-routing\] explicit nodeId unresolvable[\s\S]*?return "";\s*\}\s*const active = getActiveNode\(\);/, "unresolvable explicit nodeId must not fall back to the active node silently");

// 会话窗口拉取与关联文件 diff 按会话归属节点路由（同名根跨节点重名时裸 rootId 查表会串节点）。
const sessionViewer = fs.readFileSync(path.join(root, "src/components/SessionViewer.tsx"), "utf8");
assert.match(sessionViewer, /const sessionNodeId =\s*String\(\(session as any\)\?\._nodeId \|\| ""\)\.trim\(\) \|\| undefined;/, "SessionViewer must resolve the session's owning node");
assert.equal(sessionViewer.match(/getSessionWindow\(\s*rootId \|\| "",\s*sessionKey,\s*\{[^}]*nodeId: sessionNodeId/g)?.length, 3, "all three getSessionWindow calls must carry the session node");
assert.match(sessionViewer, /useRelatedFileStats\(\s*rootId,\s*relatedFiles,\s*gitStatsRefreshKey,\s*sessionNodeId,/, "related-file stats must route by the session node");

const relatedHook = fs.readFileSync(path.join(root, "src/hooks/useRelatedFileStats.ts"), "utf8");
assert.match(relatedHook, /refreshKey = "",\s*nodeId\?: string,/, "related-file stats hook must accept a node override");
assert.match(relatedHook, /fetchGitRelatedFileDiff\(rootId, file, nodeId \|\| undefined\)/, "related-file stats must pass the session node to the diff fetch");

const appSource = fs.readFileSync(path.join(root, "src/App.tsx"), "utf8");
assert.match(appSource, /const relatedSessionNodeId =\s*String\(\(relatedSessionSnapshot as any\)\?\._nodeId \|\| ""\)\.trim\(\) \|\| undefined;/, "App must resolve the related session's node");
assert.match(appSource, /useRelatedFileStats\(\s*relatedSessionRootId \|\| currentRootId,\s*selectedSessionRelatedFiles,\s*gitStatsRefreshKey,\s*relatedSessionNodeId,/, "App related-file stats must route by the related session's node");

console.log("node-url-normalize.test.mjs: OK");
