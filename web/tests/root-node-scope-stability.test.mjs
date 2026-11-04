// 作用域键稳定：`node::root` 不能在「同一个项目、未传 nodeId」时漂移。
//
// 症状链：selectRootNode(root) 不传 nodeId → currentRootNodeIdRef 被无条件置空 →
// scopedRootKey() 从 `fn::CMAI` 变成 `CMAI` → 绑定/选中/抽屉/cache 一起变孤儿，
// 而主面板的转录还在屏幕上 —— 发送时找不到绑定，于是新开一条会话。
//
// 这是 hook 内部的一段赋值，无法脱离 React 运行时实跑；按仓库既有做法做源码合同检查，
// 并用行为复刻证明「沿用 prevNid」与「跨节点切换仍清空」两种语义。
import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "vitest";

const root = path.resolve(fileURLToPath(import.meta.url), "../..");
const src = fs.readFileSync(path.join(root, "src/app/useAppRootSelection.ts"), "utf8");

test("同 root 未传 nodeId 时沿用上一个节点，而不是置空", () => {
  assert.ok(
    src.includes('const resolvedNid = nid || (isSameRootSwitch ? prevNid : "");'),
    "必须引入 resolvedNid 承接「沿用」语义",
  );
  assert.ok(
    src.includes("currentRootNodeIdRef.current = resolvedNid || null;"),
    "当前节点必须写 resolvedNid",
  );
  assert.ok(
    !src.includes("currentRootNodeIdRef.current = nid || null;"),
    "不得恢复成「不给 nodeId 就清空」",
  );
  assert.ok(
    src.includes("setRootNodeId(rid, resolvedNid || undefined);"),
    "模块级 root→node 映射也必须按 resolvedNid 写，否则路由与作用域各说各话",
  );
});

test("跨节点切换（两个 nodeId 都给定且不同）仍然清状态", () => {
  assert.match(
    src,
    /if \(prevNid && nid && prevNid !== nid\) \{[\s\S]*?selectedSessionByRootRef\.current\[scopedRid\] = ""/,
    "同名项目换机器必须清选中/绑定/抽屉/cache，这条不能被上面的「沿用」吞掉",
  );
});

// 行为复刻：resolvedNid 的两种输入
function resolveNid({ prevNid, nid, sameRoot }) {
  return (nid || (sameRoot ? prevNid : "")) || null;
}

test("行为：同 root 无 nodeId → 保持；新 root 无 nodeId → 空；跨节点 → 新节点", () => {
  assert.equal(resolveNid({ prevNid: "fn-node", nid: "", sameRoot: true }), "fn-node");
  assert.equal(resolveNid({ prevNid: "fn-node", nid: "", sameRoot: false }), null);
  assert.equal(resolveNid({ prevNid: "fn-node", nid: "pc-node", sameRoot: true }), "pc-node");
});
