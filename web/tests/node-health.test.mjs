// 节点可达性：一台机器「现在连不连得上」只允许有一个答案。
//
// 背景（2026-11-22，Scope: G-PF / T18）：不可达此前是各用各的临时判断 ——
// 工作台的 unreachableNodes、多项目会话组的 `_unreachable`、节点面板的 probeStatus，
// 三处各记一份；文件树则完全没有。更糟的是节点表拉取失败被 `catch(() => {})` 吞掉，
// 于是「清单可能不是最新」和「你确实没有项目」在界面上长得一模一样。
//
// 这里钉四层：
//   1. `services/net/nodeHealth` 的记录语义（去重、快照引用稳定、订阅不互相拖垮）；
//   2. `shared/nodeAvailability` 的三态判定（未观测 ≠ 离线，节点表失败优先）；
//   3. `groupEntriesByNode` 的逐节点标记；
//   4. 各条数据通路真的用了它（源码合同：hook/组件无法脱离 React 实跑）。
import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { beforeEach, test } from "vitest";
import {
  markNodeReachable,
  markNodeTableReachable,
  markNodeTableUnreachable,
  markNodeUnreachable,
  nodeHealthSnapshot,
  resetNodeHealth,
  subscribeNodeHealth,
} from "../src/services/net/nodeHealth.ts";
import { classifyNodesAvailability, isNodeUnreachable } from "../src/shared/nodeAvailability.ts";
import { groupEntriesByNode } from "../src/components/file/FileTreeEntryFilters.ts";

const root = path.resolve(import.meta.dirname, "..");
const read = (rel) => fs.readFileSync(path.join(root, rel), "utf8");

beforeEach(() => {
  resetNodeHealth();
});

// ——————————————————————— 1. 健康记录 ———————————————————————

test("节点健康表：成功/失败都记，未知 id 不算任何一台机器", () => {
  markNodeReachable("pc");
  assert.equal(nodeHealthSnapshot().nodes.pc.status, "online");
  markNodeUnreachable("fn", "boom");
  assert.deepEqual(
    { status: nodeHealthSnapshot().nodes.fn.status, detail: nodeHealthSnapshot().nodes.fn.detail },
    { status: "offline", detail: "boom" },
  );
  // 聚合请求（不带 nodeId）不代表任何一台机器，不能把空串当成一个节点。
  markNodeReachable("");
  markNodeUnreachable("   ");
  assert.deepEqual(Object.keys(nodeHealthSnapshot().nodes).sort(), ["fn", "pc"]);
});

test("节点健康表：状态没变就不 bump 版本，快照引用保持不变", () => {
  markNodeReachable("pc");
  const first = nodeHealthSnapshot();
  markNodeReachable("pc");
  assert.equal(nodeHealthSnapshot(), first, "重复成功不该重建快照（一轮刷新里每台都会成功一次）");
  markNodeUnreachable("fn", "same");
  const offline = nodeHealthSnapshot();
  markNodeUnreachable("fn", "same");
  assert.equal(nodeHealthSnapshot(), offline, "同样的失败原因重复记录不该重建快照");
  // 内容真的变了才换引用，否则 useSyncExternalStore 会无谓重渲染。
  markNodeUnreachable("fn", "other");
  assert.notEqual(nodeHealthSnapshot(), offline);
  assert.ok(nodeHealthSnapshot().version > offline.version);
});

test("节点健康表：一个订阅者抛异常不影响其它订阅者", () => {
  const seen = [];
  const off1 = subscribeNodeHealth(() => {
    throw new Error("subscriber blew up");
  });
  const off2 = subscribeNodeHealth(() => seen.push(nodeHealthSnapshot().nodes.pc?.status));
  markNodeReachable("pc");
  assert.deepEqual(seen, ["online"]);
  off1();
  off2();
  markNodeUnreachable("pc", "after unsubscribe");
  assert.deepEqual(seen, ["online"], "退订后不该再收到通知");
});

test("节点健康表：节点表成功与失败分开记，reset 清空", () => {
  markNodeTableUnreachable("nodes.json 404");
  assert.equal(nodeHealthSnapshot().nodeTable.status, "offline");
  markNodeTableReachable();
  assert.equal(nodeHealthSnapshot().nodeTable.status, "online");
  markNodeReachable("pc");
  resetNodeHealth();
  assert.deepEqual(nodeHealthSnapshot().nodes, {});
  assert.equal(nodeHealthSnapshot().nodeTable, null);
});

// ——————————————————————— 2. 三态判定 ———————————————————————

test("可用性判定：节点表失败最优先，其次是「每台都离线」", () => {
  const nodes = { pc: { status: "offline" }, fn: { status: "offline" } };
  assert.equal(
    classifyNodesAvailability({ nodeIds: ["pc", "fn"], nodes, nodeTable: { status: "offline" } }),
    "table-unreachable",
    "节点表都拉不到时，这份清单本身就不可信",
  );
  assert.equal(classifyNodesAvailability({ nodeIds: ["pc", "fn"], nodes, nodeTable: null }), "all-unreachable");
  assert.equal(classifyNodesAvailability({ nodeIds: ["pc", "fn"], nodes, nodeTable: { status: "online" } }), "all-unreachable");
});

test("可用性判定：未观测不算离线，一台通就算 ok", () => {
  assert.equal(classifyNodesAvailability({ nodeIds: [], nodes: {}, nodeTable: null }), "ok");
  assert.equal(
    classifyNodesAvailability({ nodeIds: ["pc", "fn"], nodes: { pc: { status: "offline" } }, nodeTable: null }),
    "ok",
    "fn 还没观测过 —— 未观测不等于离线，不能报「全都连不上」",
  );
  assert.equal(
    classifyNodesAvailability({ nodeIds: ["pc", "fn"], nodes: { pc: { status: "offline" }, fn: { status: "online" } }, nodeTable: null }),
    "ok",
  );
  // 空白 id 不能把「每台都离线」稀释成 ok。
  assert.equal(
    classifyNodesAvailability({ nodeIds: ["pc", "  "], nodes: { pc: { status: "offline" } }, nodeTable: null }),
    "all-unreachable",
  );
});

test("单台判定：空 id 与未观测都返回 false", () => {
  const nodes = { pc: { status: "offline" }, fn: { status: "online" } };
  assert.equal(isNodeUnreachable("pc", nodes), true);
  assert.equal(isNodeUnreachable("fn", nodes), false);
  assert.equal(isNodeUnreachable("home", nodes), false);
  assert.equal(isNodeUnreachable("", nodes), false);
  assert.equal(isNodeUnreachable(null, nodes), false);
  assert.equal(isNodeUnreachable(undefined, nodes), false);
});

// ——————————————————————— 3. 逐节点分组标记 ———————————————————————

const entry = (name, nodeId, nodeName = nodeId) => ({ name, path: `/p/${name}`, is_dir: true, _nodeId: nodeId, _nodeName: nodeName });

test("分组：只标记连不上的那台，其它组不受影响", () => {
  const entries = [entry("a", "pc", "PC"), entry("b", "fn", "fn"), entry("c", "pc", "PC")];
  const groups = groupEntriesByNode(entries, () => [{ name: "PC" }, { name: "fn" }], (id) => id === "pc");
  const byName = Object.fromEntries(groups.map((g) => [g.name, g]));
  assert.equal(byName.PC.unreachable, true);
  assert.equal(byName.fn.unreachable, false);
  assert.equal(byName.PC.items.length, 2, "标记不可达不等于丢条目");
  assert.deepEqual(groups.map((g) => g.name), ["PC", "fn"], "节点顺序仍按注册表");
});

test("分组：没有 isUnreachable 注入时不标记任何组（纯函数默认安全）", () => {
  const groups = groupEntriesByNode([entry("a", "pc", "PC")], () => []);
  assert.equal(groups[0].unreachable, false);
});

test("分组：没有 _nodeId 的条目（单节点聚合结果）不会被误标", () => {
  const groups = groupEntriesByNode([{ name: "a", path: "/p/a", is_dir: true }], () => [], () => true);
  assert.equal(groups[0].nid, "");
  assert.equal(groups[0].unreachable, false, "归属未知的条目不能凭「有节点离线」就标成不可达");
});

// ——————————————————————— 4. 数据通路（源码合同） ———————————————————————

test("节点表拉取失败不再被静默吞掉", () => {
  const src = read("src/app/managedRootNodeHealth.ts");
  assert.doesNotMatch(src, /syncNodesFromServer\(\)\.catch\(\(\) => \{\}\)/, "又被吞回去了：失败必须落到 nodeHealth");
  // 判成败只能看 source：syncNodesFromServer 失败时退回缓存/本地兜底照常返回，
  // 「没抛异常」不等于「拉到了」。
  assert.match(src, /outcome\.source === "server"/, "只有 source=server 才算拉到了");
  assert.match(src, /markNodeTableReachable\(\)/, "成功没记 → 恢复后仍显示「可能不是最新」");
  assert.match(src, /markNodeTableUnreachable\(/, "节点表失败必须记下来");
});

test("逐节点请求把成功与失败都记进健康表，并保留失败提示", () => {
  const src = read("src/app/managedRootNodeHealth.ts");
  assert.match(src, /protectedJSON<ManagedRootPayload\[\]>\(appPath\("\/api\/dirs", node\.id\)\)\);\s*markNodeReachable\(node\.id\);/, "成功没记 → 恢复后仍是离线");
  assert.match(src, /markNodeUnreachable\(node\.id,/, "失败没记 → 界面无法区分「真空」与「连不上」");
  assert.match(src, /failures\.push\(\{ id: String\(node\.id\), name:/, "失败提示（toast）不能因为加健康表而丢掉");
  // 抽成独立模块之后，协调器必须真的用它 —— 否则新模块写得再对也没人调。
  const coordinator = read("src/app/useManagedRoots.ts");
  assert.match(coordinator, /loadNodeManagedRoots\(n, nodeFailures\)/);
  assert.match(coordinator, /await syncNodeTableHealth\(opts\?\.force\)/);
});

test("节点面板的探活也并进同一份健康表", () => {
  const src = read("src/components/account/useNodeManagerPanel.ts");
  assert.match(src, /markNodeReachable\(nodeId\);/);
  assert.match(src, /markNodeUnreachable\(nodeId, "probe"\)/);
  assert.match(src, /return "online";/, "面板自己的三态（含 checking）不能被替换");
});
