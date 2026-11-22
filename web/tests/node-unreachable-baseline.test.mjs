// 节点不可达：界面必须说「连不上」，不能把项目从列表里静默抹掉。
//
// 2026-11-21 离线优先方案（Scope: G-PF）的 P0 基线把「已经做对的」与「仍然缺的」
// 分开钉住；2026-11-22 T18 把「仍然缺的」补齐后，本文件改成**正向断言** ——
// 缺的那两条现在必须成立，改坏就红：
//   ① 节点表拉取失败不再被 `catch(() => {})` 吞掉，而是记进 nodeHealth 并提示可能不是最新；
//   ② 零项目分支区分「真空」与「全都连不上」，不再无条件显示「添加一个项目开始 vibe」。
//
// 记录语义本身的行为用例在 `node-health.test.mjs`；这里钉的是「这条通路真的接上了」。
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "vitest";

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");
const managedRoots = read("src/app/useManagedRoots.ts");
const nodeHealthIo = read("src/app/managedRootNodeHealth.ts");
const projectContent = read("src/components/file/FileTreeProjectContent.tsx");
const nodeGroups = read("src/components/file/useFileTreeNodeGroups.ts");
const treeEntries = read("src/components/file/fileTreeEntries.tsx");
const fileTree = read("src/components/file/FileTree.tsx");

test("单节点失败：提示照旧，且同时记进节点健康表", () => {
  assert.match(
    nodeHealthIo,
    /catch \(err\) \{[\s\S]{0,200}?markNodeUnreachable\(node\.id,[\s\S]{0,200}?failures\.push\(\{ id: String\(node\.id\), name: String\(node\.name \|\| node\.id\) \}\);\s*return \[\] as ManagedRootPayload\[\];/,
    "失败分支的形状变了：健康记录与可见提示必须都还在",
  );
  assert.match(
    managedRoots,
    /for \(const failed of nodeLoadFailuresRef\.current\) \{\s*notifyNodeLoadFailedRef\.current\(failed\);/,
    "失败节点不再弹提示：用户会把「连不上」看成「没有项目」",
  );
});

test("失败节点的项目条目会被保留，不能整表替换", () => {
  assert.match(
    managedRoots,
    /const failedIds = new Set\(nodeLoadFailuresRef\.current\.map\(\(f\) => String\(f\.id \|\| ""\)\)\);/,
    "保留失败节点条目的判据不见了",
  );
  assert.match(
    managedRoots,
    /if \(nid && failedIds\.has\(nid\)\) preserved\[key\] = entry as ManagedRootPayload;/,
    "失败节点的项目不再保留：工作台/文件树里会出现「项目凭空消失」",
  );
});

test("节点表拉取失败：记录并提示「可能不是最新」，不再静默吞掉", () => {
  assert.doesNotMatch(nodeHealthIo, /syncNodesFromServer\(\)\.catch\(\(\) => \{\}\)/, "又被吞回去了");
  assert.match(nodeHealthIo, /markNodeTableUnreachable\(/, "节点表失败必须落到 nodeHealth");
  assert.match(
    projectContent,
    /nodesAvailability === "table-unreachable"[\s\S]{0,120}?fileTree\.nodesTableUnreachable/,
    "记了却没人显示，等于没记",
  );
});

test("零项目不再无条件说「添加一个项目」：三种空状态分开", () => {
  assert.match(managedRoots, /if \(nextRootIds\.length === 0\) \{[\s\S]*?setMainEntries\(\[\]\);/, "零项目仍要清空主区（行为不变）");
  assert.match(projectContent, /fileTree\.nodesUnreachable/, "所有节点都连不上时要说「读不到」，不能说「你没有项目」");
  assert.match(projectContent, /fileTree\.emptyProjectHint/, "真的空的时候原提示还在");
});

test("文件树逐节点标记不可达：分组带标记、无缓存条目时单独列出来", () => {
  assert.match(nodeGroups, /isNodeUnreachable\(nodeId, health\.nodes\)/, "分组标记没接健康表");
  assert.match(nodeGroups, /unreachableNodes/, "没有缓存条目的不可达节点会从界面上彻底消失");
  assert.match(nodeGroups, /classifyNodesAvailability/, "空状态判定没接");
  assert.match(fileTree, /unreachableNodeIds: new Set\(nodeGroups\.groups\.filter\(\(group\) => group\.unreachable\)/, "行内禁用没接分组标记");
  assert.match(fileTree, /unreachableNodes=\{nodeGroups\.unreachableNodes\}/);
});

test("不可达节点的行不给点：光标与点击都收口在同一处判断", () => {
  assert.match(treeEntries, /const nodeDead = !!\(unreachableNodeIds as Set<string> \| undefined\)\?\.has\(entryNodeId\);/);
  assert.match(treeEntries, /const handleEntryClick = \(\) => \{\s*if \(nodeDead\) return;/, "点了只会再失败一次");
  assert.match(treeEntries, /cursor: nodeDead \? "not-allowed" : "pointer"/);
});
