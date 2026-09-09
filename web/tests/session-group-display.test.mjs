import assert from "node:assert/strict";
import { resolveGroupColor, summarizeGroupsForLog } from "../src/services/sessionGroupDisplay.ts";

// byKey 命中：优先 nid::rid 复合键的 _nodeColor
const byKey = {
  "pc::proj-a": { _nodeColor: "#c9b84a" },
  "home::proj-b": { _nodeColor: "#7c6bd6" },
};
const nodes = [
  { id: "local", name: "local", color: "#3b82f6" },
  { id: "pc", name: "PC", color: "#f59e0b" },
];

assert.equal(resolveGroupColor({ rootId: "proj-a", _nodeId: "pc" }, byKey, nodes), "#c9b84a");
assert.equal(resolveGroupColor({ rootId: "proj-b", _nodeId: "home" }, byKey, nodes), "#7c6bd6");

// byKey 缺失：按节点 id 从 nodes 解析（名色而非回退蓝）
assert.equal(resolveGroupColor({ rootId: "proj-x", _nodeId: "pc" }, byKey, nodes), "#f59e0b");
// byKey 缺失且 id 不中：按节点 name 解析
assert.equal(resolveGroupColor({ rootId: "proj-y", _nodeId: "pc", _nodeName: "PC" }, {}, nodes), "#f59e0b");
// 全部缺失：null（由调用方决定回退，日志标记 FALLBACK）
assert.equal(resolveGroupColor({ rootId: "proj-z", _nodeId: "ghost" }, {}, nodes), null);
assert.equal(resolveGroupColor({ rootId: "", _nodeId: "" }, {}, nodes), null);

// 日志摘要三元组：rootId / rootName / nid / color / sessions
const summary = summarizeGroupsForLog(
  [
    { rootId: "proj-a", rootName: "A", _nodeId: "pc", sessions: [1, 2, 3] },
    { rootId: "proj-z", rootName: "Z", _nodeId: "ghost", sessions: [] },
  ],
  byKey,
  nodes,
);
assert.deepEqual(summary, [
  { rootId: "proj-a", rootName: "A", nid: "pc", color: "#c9b84a", sessions: 3 },
  { rootId: "proj-z", rootName: "Z", nid: "ghost", color: "FALLBACK#3b82f6", sessions: 0 },
]);
