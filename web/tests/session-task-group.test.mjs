import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "vitest";

import { orderTaskGroupedRows, taskSessionGroupLabel } from "../src/components/session/sessionTaskGroups.ts";
import {
  setWorkflowNodeStartsNewSession,
  workflowNodeStartsNewSession,
} from "../src/shared/workflowModel.ts";

const read = (relative) => fs.readFileSync(path.resolve(import.meta.dirname, "../src", relative), "utf8");
const session = (key, extra = {}) => ({ key, session_key: key, ...extra });

// 2026-11-20 用户决策（方案 A）：UI 层按 task_id 分组，零数据改动，组内仍是
// 原来的子会话样式。分组只解决「同一任务的会话散落在列表各处」，不改变归属本身。
test("同任务的顶层会话收成一组并保留原有相对顺序", () => {
  const items = [
    session("a1", { task_id: "task-a", name: "任务 A · 第一条" }),
    session("b1", { task_id: "task-b", name: "任务 B · 唯一一条" }),
    session("a2", { task_id: "task-a", name: "任务 A · 第二条" }),
    session("loose", { name: "手工会话" }),
  ];
  const rows = orderTaskGroupedRows(items);
  assert.deepEqual(
    rows.map((row) => (row.kind === "group" ? `group:${row.taskId}` : `${row.nested ? "-" : ""}${row.session.key}`)),
    ["group:task-a", "-a1", "-a2", "b1", "loose"],
    "分组出现在第一个成员的位置，成员连续排布其余顺序不变",
  );
  assert.equal(rows[0].label, "任务 A");
  assert.equal(rows[0].count, 2);
  assert.equal(rows[3].nested, false, "单条会话的任务不成组，也不缩进");
  assert.equal(rows[4].nested, false, "非任务会话保持原样");
});

test("只有一个会话的任务与空 task_id 都不分组", () => {
  const rows = orderTaskGroupedRows([
    session("only", { task_id: "task-solo" }),
    session("blank", { task_id: "  " }),
  ]);
  assert.equal(rows.length, 2);
  assert.ok(rows.every((row) => row.kind === "session" && row.nested === false));
});

test("分组头剥回任务称呼：共享会话名与节点会话名都认", () => {
  assert.equal(taskSessionGroupLabel([session("x", { name: "重构看板 · 把归档做完" })]), "重构看板");
  assert.equal(taskSessionGroupLabel([session("x", { name: "重构看板 / #7 · 把归档做完" })]), "重构看板");
  assert.equal(taskSessionGroupLabel([session("x", {}), session("y", { name: "有名字的那条" })]), "有名字的那条");
  assert.equal(taskSessionGroupLabel([session("x", {})]), "");
});

test("两条侧栏都渲染分组头，并把 nested 交给会话卡", () => {
  const single = read("components/session/SessionList.tsx");
  const multi = read("components/session/MultiProjectSessionList.tsx");
  for (const [name, source] of [["SessionList", single], ["MultiProjectSessionList", multi]]) {
    assert.match(source, /row\.type === "task-group"/, `${name} must branch on task-group rows`);
    assert.match(source, /<TaskSessionGroupHeader/, `${name} must render the group header`);
    assert.match(source, /nested=\{row\.nested\}/, `${name} must indent grouped sessions`);
  }
  // 搜索结果是平铺的命中列表，按任务重组会让「第 N 条命中」跳位。
  assert.match(single, /searchResultsMode\s*\?\s*sessions\.map/, "search results must stay flat");
});

test("nested 只借缩进，不改子会话语义", () => {
  const card = read("components/session/SessionCard.tsx");
  const hook = read("components/session/useSessionCard.ts");
  assert.match(card, /paddingLeft: isSubagent \|\| nested \? "10px" : 0/, "grouped rows share the subagent indent");
  assert.match(hook, /const isSubagent = !!session\.parent_session_key/, "isSubagent still derives only from the parent key");
  assert.doesNotMatch(hook, /nested/, "nested must not leak into the subagent flag");
});

// 后端 SessionReuseRun 是默认值；节点级勾选是唯一的显式退出方式。
test("节点「另起会话」写 always_new，取消则回到 Run 共享会话", () => {
  assert.equal(workflowNodeStartsNewSession({ id: "n", kind: "agent" }), false, "缺省与其他节点共用会话");
  assert.equal(workflowNodeStartsNewSession({ id: "n", kind: "agent", params: { session_reuse_policy: "run" } }), false);
  assert.equal(workflowNodeStartsNewSession({ id: "n", kind: "agent", params: { session_reuse_policy: "per_node" } }), true);
  assert.equal(workflowNodeStartsNewSession({ id: "n", kind: "agent", params: { session_reuse_policy: "always_new" } }), true);

  const on = setWorkflowNodeStartsNewSession({ prompt: "实现" }, true);
  assert.equal(on.session_reuse_policy, "always_new");
  assert.equal(on.prompt, "实现", "other params must be preserved");
  const off = setWorkflowNodeStartsNewSession(on, false);
  assert.equal("session_reuse_policy" in off, false, "unchecking removes the param so the run session applies");
});

test("节点属性面板接线到同一组判据", () => {
  const inspector = read("components/task/WorkflowNodeInspector.tsx");
  assert.match(inspector, /data-testid="workflow-node-new-session"/);
  assert.match(inspector, /checked=\{workflowNodeStartsNewSession\(selected\)\}/);
  assert.match(inspector, /setWorkflowNodeStartsNewSession\(selected\.params, e\.target\.checked\)/);
});
