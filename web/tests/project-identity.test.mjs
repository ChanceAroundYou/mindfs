// 项目身份 = (nodeId, rootId)：不同节点的同名 / 同路径项目必须被当成两个项目。
//
// 现场（2026-11-20）：fn 与 pc 各有一个 id 为 `docs` 的项目；`indexManagedRoots`
// 的 byId 是「首个获胜」，于是裸 rootId 反查永远落到列表里排在前面的那台。任务卡
// 动作打到另一台机器的空库 → `sql: no rows in result set` → 卡片既不能完成也不能
// 删除（G-NP 修了卡片动作，G-NQ 修身份本身）。
//
// 这里钉两件事：
//   1. 判定规则的**行为**（resolveProjectNodeId / rootNode 模块级解析）；
//   2. 各条数据通路真的按项目身份走（源码合同，hook 内部无法脱离 React 实跑）。
import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";
import { resolveProjectNodeId } from "../src/shared/projectIdentity.ts";
import {
  getRootNodeId,
  rootNodeOwners,
  setRootNodeId,
  setRootNodeMap,
} from "../src/services/net/rootNode.ts";

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

// ——————————————————————————— 1. 判定规则 ———————————————————————————

test("resolveProjectNodeId：显式节点最权威，空串不算数", () => {
  const nodesForRoot = () => ["pc"];
  assert.equal(
    resolveProjectNodeId({ rootId: "docs", explicitNodeId: "fn", nodesForRoot }),
    "fn",
    "调用方知道节点时不能被子查询覆盖",
  );
  assert.equal(
    resolveProjectNodeId({ rootId: "docs", explicitNodeId: "  ", nodesForRoot }),
    "pc",
    "空白显式节点视为未提供",
  );
});

test("resolveProjectNodeId：当前选中的项目按当前节点解析", () => {
  assert.equal(
    resolveProjectNodeId({
      rootId: "docs",
      currentRootId: "docs",
      currentNodeId: "fn",
      nodesForRoot: () => ["pc", "fn"],
    }),
    "fn",
    "同名项目里，正在看的那个必须按用户选中的机器走，而不是按归属表顺序",
  );
});

test("resolveProjectNodeId：唯一归属才猜，多台都有就不猜", () => {
  assert.equal(
    resolveProjectNodeId({ rootId: "docs", nodesForRoot: () => ["fn"] }),
    "fn",
    "只有一台机器有这个项目时，裸 rootId 是明确的",
  );
  assert.equal(
    resolveProjectNodeId({ rootId: "docs", nodesForRoot: () => ["pc", "fn"] }),
    undefined,
    "两台机器都有 `docs` 时必须交给显式 nodeId，不能「首个获胜」",
  );
  assert.equal(
    resolveProjectNodeId({ rootId: "docs", nodesForRoot: () => ["fn", "fn"] }),
    "fn",
    "重复归属先去重再判唯一",
  );
  assert.equal(
    resolveProjectNodeId({ rootId: "", nodesForRoot: () => ["fn"] }),
    undefined,
    "没有 rootId 时任何节点都是猜",
  );
  assert.equal(
    resolveProjectNodeId({ rootId: "docs", nodesForRoot: () => [] }),
    undefined,
    "注册表里没有它时不猜",
  );
});

// ——————————————————————— 2. 模块级 root→node 路由表 ———————————————————————

test("rootNode：同名项目的裸 rootId 解析不出来，选中之后才解析得出", () => {
  setRootNodeMap({
    "pc::docs": { id: "docs", _nodeId: "pc" },
    "fn::docs": { id: "docs", _nodeId: "fn" },
    "local::mindfs": { id: "mindfs", _nodeId: "local" },
  });
  assert.deepEqual(rootNodeOwners("docs").sort(), ["fn", "pc"], "归属表要保留两台");
  assert.equal(getRootNodeId("docs"), undefined, "两台都有 docs 时不能猜");
  assert.equal(getRootNodeId("mindfs"), "local", "唯一归属照旧可解析");
  setRootNodeId("docs", "fn");
  assert.equal(getRootNodeId("docs"), "fn", "选中 fn 的 docs 之后按 fn 路由");
  setRootNodeId("docs", undefined);
  assert.equal(getRootNodeId("docs"), undefined, "取消选中后回到「解析不出」");
  assert.equal(getRootNodeId("docs", "pc"), "pc", "显式 nodeId 永远压过选中与归属");
});

test("rootNode：不带 _nodeId 的条目只说明本地有一份，不参与归属判断", () => {
  setRootNodeMap({ docs: { id: "docs" } });
  assert.deepEqual(rootNodeOwners("docs"), [], "零节点回落的条目不能让裸 rootId 变成「唯一归属」");
  assert.equal(getRootNodeId("docs"), undefined);
});

// ————————————————————— 3. 索引与各条数据通路的源码合同 —————————————————————
// hook 内部状态脱离 React 跑不起来；按仓库既有做法钉住关键实现，并用行为测试
// 覆盖纯函数部分。

test("indexManagedRoots：byId 只装唯一归属，不再首个获胜", () => {
  const misc = read("src/app/appMisc.ts");
  assert.match(misc, /nodesById/, "需要一张 rootId → 拥有它的节点列表");
  assert.match(
    misc,
    /if \(owners\.length === 1\) byId\[id\] = byKey\[rootNodeKey\(owners\[0\], id\)\];/,
    "byId 只在唯一归属时填那一格 —— 这是整个身份判定的根",
  );
  assert.ok(
    !/byId\[id\] = dir/.test(misc),
    "不得恢复「首个获胜」：那正是 fn 的卡打到 pc 的原因",
  );
  assert.match(
    misc,
    /export function managedRootsForIds\(/,
    "按 id 重建根条目时必须能带上同名项目的另一份（byId 已经装不下它）",
  );
});

test("refreshManagedRoots：同名项目不能被当成「已删除」清空状态", () => {
  const src = read("src/app/useManagedRoots.ts");
  assert.match(
    src,
    /const nextRootIdSet = new Set\(nextRootIds\);/,
    "存在性判断要基于全集",
  );
  assert.match(
    src,
    /if \(!nextRootIdSet\.has\(rootID\)\) \{[\s\S]*?clearRootScopedClientState/,
    "只有项目真的没了才清根作用域；按 byId 判会把同名项目每刷新一次清一遍",
  );
  assert.match(
    src,
    /if \(nid && failedIds\.has\(nid\)\) preserved\[key\] = entry/,
    "本轮拉取失败的节点要保住它的项目，不能整表替换掉",
  );
});

test("pruneTaskDetails：按节点裁剪，别人节点的任务不许被误删", () => {
  const board = read("src/app/useTaskDetailsPrune.ts");
  assert.match(
    board,
    /return useCallback\(\(rootId: string, keepTaskIds: Iterable<string>, nodeId\?: string\) =>/,
    "裁剪必须能收到节点",
  );
  assert.match(
    board,
    /if \(!nid\) return true;\s*\n\s*const taskNid = String\(\(task as any\)\?\._nodeId \|\| ""\)\.trim\(\);\s*\n\s*return !taskNid \|\| taskNid === nid;/,
    "给了节点就必须逐条比对：同名项目的另一份原样留着，不能被一起裁掉",
  );
  assert.match(
    read("src/app/useTaskBoard.ts"),
    /const detailNid = String\(details\.find\(/,
    "整包下发的详情要按数据自带的 _nodeId 落缓存，不能按当前视图猜",
  );
});

test("详情面板：节点按任务自己的项目走", () => {
  const gate = read("src/components/app/TaskDetailPanelGate.tsx");
  assert.match(
    gate,
    /const ownNodeId = String\(selectedKanbanTask\?\._nodeId \|\| ""\)\.trim\(\);/,
    "任务自带节点优先",
  );
  assert.match(
    gate,
    /nodeId=\{taskNodeId\(selectedKanbanTask\.root_id \|\| currentRootId\)\}/,
    "面板路由必须按任务身份，不能按当前选中项目",
  );
});

test("文件类请求：/api/tree 用本请求解析出的节点", () => {
  const src = read("src/app/openDirectoryAction.ts");
  assert.match(
    src,
    /const resolvedNodeId =\s*\n?\s*String\(params\.nodeId \|\| ""\)\.trim\(\)/,
    "显式节点 → 当前节点 → 唯一归属，三级回落",
  );
  assert.match(
    src,
    /appURL\("\/api\/tree", new URLSearchParams\(\{ root, dir: apiDir \}\), resolvedNodeId \|\| undefined\)/,
    "回包缓存键落在本节点桶里，请求也必须打本节点",
  );
});

test("工作台：分组名与颜色按分组自己的节点取", () => {
  const src = read("src/app/useWorkspaceBoard.ts");
  assert.match(
    src,
    /rootName: \(getProjectLabel \? getProjectLabel\(rootId, nodeId\) : getRootDisplayName\(rootId, nodeId\)\) \|\| rootId,/,
    "分组名要按分组节点解析；同名项目靠 getProjectLabel 加节点后缀",
  );
  assert.match(src, /^\s*nodeId,$/m, "分组要带上节点，后续动作都靠它");
  const display = read("src/app/useAppProjectDisplay.ts");
  assert.match(
    display,
    /export function|const getProjectLabel = useCallback/,
    "getProjectLabel 是显示层唯一的加后缀入口",
  );
});

test("会话侧栏：同行同名项目的 React key 必须带节点", () => {
  const src = read("src/components/session/MultiProjectSessionList.tsx");
  assert.match(src, /key=\{`task-group-\$\{groupNodeId\}::\$\{group\.rootId\}-\$\{row\.taskId\}`\}/);
  assert.match(src, /key=\{`children-toggle-\$\{groupNodeId\}::\$\{group\.rootId\}-\$\{row\.parent\.key\}`\}/);
});

test("插件表与信任记录：按 (node, root) 分桶，旧裸键只读兼容", () => {
  const manager = read("src/plugins/manager.ts");
  assert.match(manager, /return nid \? `\$\{nid\}::\$\{rid\}` : rid;/, "桶键要带节点段");
  const trust = read("src/plugins/trust.ts");
  assert.match(trust, /export function pluginTrustStorageKey\(rootId: string, nodeId\?: string\)/);
  assert.match(
    trust,
    /if \(!raw && nid\) raw = window\.localStorage\.getItem\(pluginTrustStorageKey\(rootId\)\);/,
    "带节点查不到时回退历史裸键，老记录零迁移",
  );
});

test("删项目 / 按路径找项目：走完整索引，不靠 byId 碰运气", () => {
  const src = read("src/app/useAppProjectRemoval.ts");
  assert.match(src, /managedRootByKeyRef/, "需要完整索引才能拿到同名项目的那一份");
  assert.match(
    src,
    /const rootInfo = managedRootFor\(rootID, currentRootNodeIdRef\.current \|\| getNodeIdForRoot\(rootID\)\);/,
    "删当前项目按当前选中节点取 root_path",
  );
  assert.match(
    src,
    /const pool = Object\.values\(managedRootByKeyRef\.current \|\| \{\}\);/,
    "按路径找项目时优先完整索引",
  );
});
