import assert from "node:assert/strict";
import { readFileSync, existsSync } from "node:fs";

// 工作台必须能看到**所有节点**上的项目。后端 Overview() 遍历的是本节点的 roots，
// 它不知道自己被哪个节点调用，所以跨节点只能由前端扇出 —— 这里钉住扇出的形状。
const board = readFileSync(new URL("../src/app/useWorkspaceBoard.ts", import.meta.url), "utf8");
const tasks = readFileSync(new URL("../src/services/tasks.ts", import.meta.url), "utf8");
const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const goService = readFileSync(new URL("../../server/internal/kanban/service.go", import.meta.url), "utf8");

// 1) 扇出走 Promise.all，一个节点失败不阻塞其余（与 loadMultiProjectSessionGroups 同一形状）
assert.match(
  board,
  /await Promise\.all\(\s*targets\.map\(async \(nid\) => \{[\s\S]*?try \{[\s\S]*?\} catch \{\s*return \[\] as WorkspaceTaskItem\[\];/,
  "the fan-out must use Promise.all and swallow a single node's failure",
);

// 2) 节点清单必须来自 getNodes()，并与 managedRootIds 解析出的节点取并集
//    （getNodes() 初始化竞态下可能还是空；反过来本地当前节点也可能还没进 getNodes()）
assert.match(board, /const fromNodes = getNodes\(\)/, "node list must start from the node registry");
assert.match(
  board,
  /Array\.from\(new Set\(\[\.\.\.fromNodes, \.\.\.fromRoots\]\)\)/,
  "registry nodes and root-derived nodes must be unioned, not one-or-the-other",
);
assert.match(
  board,
  /const fromRoots = cfg\.managedRootIds\s*\.map\(\(rid\) => String\(cfg\.getNodeId\(rid\) \|\| ""\)\.trim\(\)\)/,
  "the second node source must be derived from managed roots",
);
// 全空时回退到激活节点，与会话侧 loadMultiProjectSessionGroups 同一口径
assert.match(
  board,
  /\[String\(cfg\.fallbackNodeId \|\| ""\)\.trim\(\)\]\.filter\(Boolean\)/,
  "an empty node list must fall back to the active node rather than skipping the fetch",
);

// 3) 去重键必须带 nodeId：同名项目在两台机器上各算一条，不是同一条
assert.match(
  board,
  /const key = `\$\{item\.nodeId\}::\$\{item\.root_id\}::\$\{item\.task\.id\}`;/,
  "dedup key must be nodeId::rootId::taskId so homonymous projects on different nodes stay distinct",
);

// 4) 聚合以 managedRootIds 为基准，不是以返回的 items 为基准 ——
//    后端只 append 有任务的项目，拿 items 建组会让空项目从工作台上消失
assert.match(
  board,
  /return toRootEntries\(managedRootIds, getNodeId\)/,
  "groups must be seeded from managedRootIds, not from the fetched items",
);
assert.match(board, /export function toRootEntries|function toRootEntries/, "the seeding helper must exist");
assert.match(
  board,
  /return true; \/\/ 「全部」下空项目也要出现 —— 它才需要被看到/,
  "the 'all' filter must keep empty projects visible",
);

// 5) TaskOverviewItem 的 nodeId 必须是可选的：后端不返，直接用该接口的调用方类型照旧成立
assert.match(tasks, /nodeId\?: string;/, "TaskOverviewItem.nodeId must be optional");

// 6) 后端零改动守卫：Go struct 里不能出现 NodeID
assert.match(goService, /type TaskOverviewItem struct/, "the Go overview item must still exist");
assert.doesNotMatch(
  goService,
  /type TaskOverviewItem struct \{[^}]*NodeID/s,
  "the backend must not gain a NodeID field — node identity is the fan-out's job, not the server's",
);

// 7) App 侧不再裸调 fetchTasksOverview，只剩走 hook 一条路
assert.doesNotMatch(
  app,
  /fetchTasksOverview\(/,
  "App must not fetch the overview directly; the fan-out hook owns it",
);
assert.match(app, /useWorkspaceBoard\(\{/, "App must drive the board through useWorkspaceBoard");

// 8) 拉数失败不能抛给渲染层：hook 的 catch 返回空数组而不是 reject
assert.ok(
  existsSync(new URL("../src/app/useWorkspaceBoard.ts", import.meta.url)),
  "the fan-out hook must exist",
);

console.log("workspace-node-fanout.test.mjs: OK");
