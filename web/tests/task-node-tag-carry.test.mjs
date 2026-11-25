// 详情合并必须保住跨节点归属（G-RT）。
//
// 现场（2026-11-25）：pc-wsl 上的 CMAI 任务在工作台点开就报
// 「对话加载失败: root not found」，点重试仍然失败。请求链是：
//   1. 工作台按来源节点打标后打开面板，第一次取会话打到 pc-wsl（200）；
//   2. 面板自己的按编号重拉（useTaskDetailPanel）拿回一份**不带 `_nodeId`**
//      的裸详情，onMoved → applyTaskDetails 直接覆盖在册记录；
//   3. 面板 key 含 `_nodeId`，标没了就重挂，nodeId 变 undefined，appURL 回落到
//      active node（本机）；本机没有 CMAI root → activities 500 root not found。
//   4. 顺带把详情缓存写成 `local::CMAI::...`，下次读缓存同样没有归属。
//
// 这里钉两件事：
//   1. carryTaskNodeTag 的**行为**（数据自带优先 / 缺失沿用 / 不变异入参）；
//   2. 合并入口 applyTaskDetails 真的先补标，再算节点隔离与缓存前缀（源码合同，
//      hook 内部无法脱离 React 实跑）。
import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import { test } from "vitest";
import { carryTaskNodeTag } from "../src/shared/taskNodeTag.ts";

const webRoot = path.resolve(import.meta.dirname, "..");
const readWeb = (rel) => fs.readFileSync(path.join(webRoot, rel), "utf8");

const detail = (tid, nodeId) => ({
  task: { id: tid, updated_at: "2026-11-25T11:52:00Z", ...(nodeId ? { _nodeId: nodeId } : {}) },
  events: [],
});

test("裸详情继承在册归属：服务端不返 _nodeId，刷新不能把标抹掉", () => {
  const merged = carryTaskNodeTag(detail("task_a", undefined), detail("task_a", "pc-wsl"));
  assert.equal(merged.task._nodeId, "pc-wsl");
  assert.equal(merged.events.length, 0, "其余字段原样保留");
});

test("数据自带 _nodeId 时以数据为准，不被旧记录覆盖", () => {
  const merged = carryTaskNodeTag(detail("task_a", "fn"), detail("task_a", "pc-wsl"));
  assert.equal(merged.task._nodeId, "fn");
});

test("两处都没有 _nodeId 时不凭空造节点", () => {
  assert.equal(carryTaskNodeTag(detail("task_a", undefined), undefined).task._nodeId, undefined);
  assert.equal(carryTaskNodeTag(detail("task_a", undefined), detail("task_a", undefined)).task._nodeId, undefined);
  // 空白不算归属：视为缺失，沿用已在册的那份。
  assert.equal(carryTaskNodeTag(detail("task_a", "  "), detail("task_a", "pc-wsl")).task._nodeId, "pc-wsl");
});

test("不修改入参对象", () => {
  const incoming = detail("task_a", undefined);
  const previous = detail("task_a", "pc-wsl");
  const merged = carryTaskNodeTag(incoming, previous);
  assert.equal(incoming.task._nodeId, undefined, "不能就地改调用方的详情");
  assert.equal(previous.task._nodeId, "pc-wsl");
  assert.notEqual(merged.task, incoming.task);
});

test("applyTaskDetails 先补标再算节点与缓存前缀", () => {
  const src = readWeb("src/app/useTaskBoard.ts");
  const start = src.indexOf("const applyTaskDetails = useCallback");
  assert.ok(start >= 0, "找不到 applyTaskDetails");
  const body = src.slice(start, start + 4000);
  const carryAt = body.indexOf("carryTaskNodeTag(");
  const nidAt = body.indexOf("const detailNid");
  const sidAt = body.indexOf("const sid = detailNid");
  assert.ok(carryAt >= 0, "合并入口必须调用 carryTaskNodeTag");
  assert.ok(carryAt < nidAt, "补标必须发生在 detailNid 之前");
  assert.ok(carryAt < sidAt, "补标必须发生在节点隔离（const sid）之前");
  assert.match(
    body,
    /taskDetailsByIdRef\.current\[String\(detail\.task\.id\)\]/,
    "在册记录的来源必须是 taskDetailsByIdRef，而不是别的索引",
  );
});
