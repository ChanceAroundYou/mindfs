import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import { test } from "vitest";

test("node-url-normalize", async () => {
const root = path.resolve(import.meta.dirname, "..");
const sourcePath = path.join(root, "src/services/net/nodeBase.ts");
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

const registry = fs.readFileSync(path.join(root, "src/services/net/nodeRegistry.ts"), "utf8");
// 表的规整/去重与 self 行合成在 net/nodeTableNormalize.ts（nodeRegistry 只做编排）。
const tableNormalize = fs.readFileSync(path.join(root, "src/services/net/nodeTableNormalize.ts"), "utf8");
// 节点连通性探测已从 FileTree 的「添加节点」popover 搬到 NodeManagerPanel（添加 + 改地址共用
// probeCandidateUrl）；守卫跟着代码搬家，意图不变：候选地址必须用显式远端 base 直接拼，
// 不得继承本地部署前缀，否则探测打到的是本机、连通性检查形同虚设。
const nodeManager = fs.readFileSync(path.join(root, "src/components/account/NodeManagerPanel.tsx"), "utf8");
assert.match(nodeManager, /fetch\(`\$\{normalizedUrl\}\/api\/agents`/, "node probe must use the explicit remote base");
assert.doesNotMatch(nodeManager, /normalizeBaseURLWithPrefix\(/, "node probe must not inherit the local prefix");
assert.match(registry, /const cacheAtStart = cache;/, "initial sync must detect a concurrent local write");
assert.match(registry, /if \(cache !== cacheAtStart\) return \{ nodes: cache \|\| nodes, source: "server" \};/, "initial sync must not overwrite a concurrent local write");
assert.match(registry, /await enqueueServerWrite\(merged\.map\(\(n\) => \(\{ \.\.\.n, _syntheticSelf: undefined \}\)\)\);\s*nodes = merged;\s*clearLegacyLocalNodes\(\);/s, "legacy nodes must only be cleared after a successful server write");
assert.doesNotMatch(registry, /if \(syncing\) return/, "overlapping node writes must not be dropped");
assert.match(registry, /const write = writeQueue\.then\(\(\) => pushToServer\(snapshot\)\);/, "node writes must be serialized");

// 去重键必须带路径段，不能只比 origin：同一台物理机可以挂两个前缀（实测 PC 上 WSL 节点
// /mindfs 与 Windows 节点 /mindfs-win 共用 pc.xiaokubao.space），只比 origin 会把后一个节点吞掉，
// 界面里根本看不到它，「添加节点」还会报 node_url_exists。
assert.ok(tableNormalize.includes("function nodeBaseKey(url: string): string {"), "node dedup key must be path-aware, not origin-only");
assert.ok(tableNormalize.includes('const path = u.pathname.replace(/\\/+$/, "");'), "dedup key must normalize the path segment");
assert.ok(tableNormalize.includes('return `${u.protocol}//${u.host}${path}`.toLowerCase();'), "dedup key must include the deploy prefix path, not only the origin");

// self 行的地址分两套：合成行用设备推导地址；共享行保留用户配置的直连网址
// （统一后的 pc 行就是靠这条留住 /mindfs-win），本机请求的寻址另走 runtime.selfLocalBase。
assert.match(tableNormalize, /function deviceLocalNodeURL\(\): string \{\s*const base = localBaseURL\(\);\s*if \(!base \|\| !\/\^https\?:\\\/\\\/\/i\.test\(base\)\) return "";\s*return base;\s*\}/, "device-local URL must only accept fetchable http(s) origins");
assert.match(tableNormalize, /function makeSelfNode\(\): NodeConnection \{[\s\S]*?url: deviceLocalNodeURL\(\),[\s\S]*?instanceId: identity \? identity\.id : undefined,[\s\S]*?_syntheticSelf: true,[\s\S]*?\}/, "ephemeral self node must carry the device-derived URL, the server-reported identity and the synthetic marker");
assert.doesNotMatch(tableNormalize, /读取时强制按当前设备推导[\s\S]{0,80}?if \(self\) self\.url = deviceLocalNodeURL\(\);/, "shared self rows keep their configured direct URL; only routing is device-local");
assert.match(tableNormalize, /if \(!self\.url\) self\.url = deviceLocalNodeURL\(\);/, "a synthesized self row still needs the device-derived URL");

// 显式 nodeId 解析失败不得静默回退 active node：会把请求发给错误节点。
// 同时，节点地址必须走「直连优先、relay 兜底」——relay-only 节点（未配直连的边控端）
// 只有 relayUrl，只认 url 会被当成「没有地址」。
const runtimeSource = fs.readFileSync(path.join(root, "src/services/platform/runtime.ts"), "utf8");
assert.match(
  runtimeSource,
  /function resolveNodeBaseURL\(nodeId\?: string\): string \{\s*if \(nodeId\) \{\s*const node = getNodeById\(nodeId\);[\s\S]*?const base = nodeRouteBase\(node\);\s*if \(base\) return normalizeExplicitNodeBase\(base\);/,
  "explicit nodeId must resolve url || relayUrl (direct preferred, relay fallback)",
);
assert.match(
  runtimeSource,
  /console\.warn\("\[node-routing\] explicit nodeId unresolvable[\s\S]*?return "";\s*\}\s*const active = getActiveNode\(\);\s*const activeLocal = selfLocalBase\(active\);\s*if \(activeLocal\) return normalizeExplicitNodeBase\(activeLocal\);\s*const activeBase = nodeRouteBase\(active\);\s*if \(activeBase\) return normalizeExplicitNodeBase\(activeBase\);/,
  "unresolvable explicit nodeId must not fall back to the active node silently",
);
assert.match(
  runtimeSource,
  /u = normalizeExplicitNodeBase\(selfLocalBase\(node\) \|\| nodeRouteBase\(node\)\);[\s\S]*?const active = getActiveNode\(\);\s*u = normalizeExplicitNodeBase\(selfLocalBase\(active\) \|\| nodeRouteBase\(active\)\);/,
  "getWsBaseURL must derive from the same url || relayUrl resolution",
);

// 会话窗口拉取与关联文件 diff 按会话归属节点路由（同名根跨节点重名时裸 rootId 查表会串节点）。
const sessionViewer = fs.readFileSync(path.join(root, "src/components/session/SessionViewer.tsx"), "utf8");
assert.match(sessionViewer, /const sessionNodeId =\s*String\(\(session as any\)\?\._nodeId \|\| ""\)\.trim\(\) \|\| undefined;/, "SessionViewer must resolve the session's owning node");
const sessionWindowing = fs.readFileSync(path.join(root, "src/components/session/useSessionWindowing.ts"), "utf8");
assert.equal(sessionWindowing.match(/getSessionWindow\(\s*rootId \|\| "",\s*sessionKey,\s*\{[^}]*nodeId: sessionNodeId/g)?.length, 2, "all getSessionWindow calls (latest/beforeSeq) must carry the session node");
// 会话底部的关联文件面板已移除（G-SK），这条 stats 路由只剩 App 侧的根视图路径（见下方）。

const relatedHook = fs.readFileSync(path.join(root, "src/hooks/useRelatedFileStats.ts"), "utf8");
assert.match(relatedHook, /refreshKey = "",\s*nodeId\?: string,/, "related-file stats hook must accept a node override");
// 统计改走批量接口后，这条守卫钉的还是同一件事：hook 必须把会话归属节点透传下去。
// 另外把 git.ts 那一侧的解析也一起看住 —— 光透传、服务层又退回 active node，等于没透传。
assert.match(relatedHook, /fetchRelatedFileStatsBatch\(rootId, targets, nodeId \|\| undefined\)/, "related-file stats must pass the session node to the batch fetch");
const gitService = fs.readFileSync(path.join(root, "src/services/git/relatedStats.ts"), "utf8");
assert.match(gitService, /const scopedNodeId = nodeId \|\| getRootNodeId\(rootId\);/, "the batch stats fetch must resolve the session node before building the URL");
assert.match(gitService, /appURL\("\/api\/git\/related-files\/stats", undefined, scopedNodeId\)/, "the batch stats request must carry the resolved node");

const appSource = fs.readFileSync(path.join(root, "src/app/useAppViewComposition.ts"), "utf8");
assert.match(appSource, /const relatedSessionNodeId =\s*String\(\(relatedSessionSnapshot as any\)\?\._nodeId \|\| ""\)\.trim\(\) \|\| undefined;/, "App must resolve the related session's node");
const rootContentView = fs.readFileSync(path.join(root, "src/app/useRootContentView.ts"), "utf8");
assert.match(rootContentView, /useRelatedFileStats\(\s*relatedSessionRootId \|\| currentRootId,\s*selectedSessionRelatedFiles,\s*gitStatsRefreshKey,\s*relatedSessionNodeId,/, "App related-file stats must route by the related session's node");

console.log("node-url-normalize.test.mjs: OK");
});
