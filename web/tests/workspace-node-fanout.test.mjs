import assert from "node:assert/strict";
import { readFileSync, existsSync } from "node:fs";

// 工作台必须能看到**所有节点**上的项目。后端 Overview() 遍历的是本节点的 roots，
// 它不知道自己被哪个节点调用，所以跨节点只能由前端扇出 —— 这里钉住扇出的形状。
const board = readFileSync(new URL("../src/app/useWorkspaceBoard.ts", import.meta.url), "utf8");
const tasks = readFileSync(new URL("../src/services/tasks.ts", import.meta.url), "utf8");
const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const goService = readFileSync(new URL("../../server/internal/kanban/service.go", import.meta.url), "utf8");

// 1) 扇出走 Promise.all，一个节点失败不阻塞其余（与 loadMultiProjectSessionGroups 同一形状）
//    catch 分支还必须**记下失败的节点**：断联时 tasks 拉空，用户看到的是「任务凭空消失」，
//    和「那边真的没任务」长得一模一样（B3）。
assert.match(
  board,
  /await Promise\.all\(\s*targets\.map\(async \(nid\) => \{[\s\S]*?try \{[\s\S]*?\} catch \{[\s\S]*?return \[\] as WorkspaceTaskItem\[\];/,
  "the fan-out must use Promise.all and swallow a single node's failure",
);
assert.match(
  board,
  /\} catch \{\s*failedNodeIds\.push\(nid\);/,
  "a failing node must be recorded, otherwise its tasks silently vanish with no explanation",
);
assert.match(
  board,
  /setUnreachableNodes\(/,
  "unreachable nodes must be surfaced to the view layer",
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

// 4) 分组来源 = managedRootIds **∪ 任务里出现的 nodeId::root_id**。
//    纯 managedRootIds（本机 registry）当唯一基准时，非本机项目即使有任务也不成组 ——
//    「pc 上的任务全没了」就是这个：pc 恢复后它的项目不在本机清单里。
//    纯 items 又会让「一个任务都没有的项目」和「本节点没有、别的节点有同名项目」混为一谈。
//    两者取并集，建完再按匹配到的任务收窄，匹配不到就不渲染。
assert.match(
  board,
  /return toGroupEntries\(managedRootIds, getNodeId, byProject\)/,
  "groups must be seeded from managedRootIds union the nodeId::root_id pairs actually seen in items",
);
assert.match(board, /function toGroupEntries/, "the seeding helper must exist");
assert.match(
  board,
  /for \(const \[key, bucket\] of byProject\) \{[\s\S]*?if \(seen\.has\(key\) \|\| bucket\.length === 0\) continue;[\s\S]*?push\(String\(first\?\.root_id \|\| ""\), String\(first\?\.nodeId \|\| ""\)\.trim\(\)\);/,
  "the seeding helper must also emit nodeId::root_id pairs that only exist in the fetched items",
);
assert.match(
  board,
  /const key = scopeKeyForItem\(item, getNodeId\);/,
  "the bucket key must fall back to the managed-root node id when the item carries none",
);
assert.match(
  board,
  /\.filter\(\(group\) => group\.tasks\.length > 0\);/,
  "a group with no task matching the filter must not be rendered — under 「全部」 too",
);

// 5) 切到工作台那一刻必须真的发起扇出：enabled 从 false 翻到 true 的那次渲染，
//    本地节点可能还没进 getNodes()/managedRootIds —— 若 effect 只认依赖值而它们恰好没变，
//    切过去会看到一台节点都不扇出（空工作台）。所以依赖里要有节点/项目清单本身。
assert.match(
  board,
  /const fanoutKey = enabled[\s\S]*?managedRootIds\.join\(","\)/,
  "the fan-out effect must re-run when the node/project list changes, not only on the boolean flip",
);
assert.match(
  board,
  /\}, \[enabled, refreshToken, localToken, fanoutKey\]\);/,
  "fanoutKey must be an effect dependency",
);

// 6) TaskOverviewItem 的 nodeId 必须是可选的：后端不返，直接用该接口的调用方类型照旧成立
assert.match(tasks, /nodeId\?: string;/, "TaskOverviewItem.nodeId must be optional");

// 7) 后端零改动守卫：Go struct 里不能出现 NodeID
assert.match(goService, /type TaskOverviewItem struct/, "the Go overview item must still exist");
assert.doesNotMatch(
  goService,
  /type TaskOverviewItem struct \{[^}]*NodeID/s,
  "the backend must not gain a NodeID field — node identity is the fan-out's job, not the server's",
);

// 8) App 侧不再裸调 fetchTasksOverview，只剩走 hook 一条路
assert.doesNotMatch(
  app,
  /fetchTasksOverview\(/,
  "App must not fetch the overview directly; the fan-out hook owns it",
);
assert.match(app, /useWorkspaceBoard\(\{/, "App must drive the board through useWorkspaceBoard");

// 9) 拉数失败不能抛给渲染层：hook 的 catch 返回空数组而不是 reject
assert.ok(
  existsSync(new URL("../src/app/useWorkspaceBoard.ts", import.meta.url)),
  "the fan-out hook must exist",
);

console.log("workspace-node-fanout.test.mjs: OK");
