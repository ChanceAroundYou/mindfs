import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "vitest";

import {
  orderTaskGroupedRows,
  taskSessionGroupHeading,
  taskSessionGroupLabel,
  taskSessionMemberLabel,
} from "../src/components/session/sessionTaskGroups.ts";
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
  assert.equal(rows[0].count, undefined, "分组头不再带成员计数（成员本身就是计数）");
  assert.deepEqual(
    rows.filter((row) => row.kind === "session" && row.nested).map((row) => row.groupLabel),
    ["任务 A", "任务 A"],
    "成员行带上要剥掉的共享前缀",
  );
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

test("分组头文案 = 任务名 + 任务号，和会话名同一行字", () => {
  assert.equal(taskSessionGroupHeading("对话命名问题", "91", "任务会话"), "对话命名问题 # 91");
  assert.equal(taskSessionGroupHeading("", "91", "任务会话"), "任务会话 # 91");
  assert.equal(taskSessionGroupHeading("对话命名问题", "", "任务会话"), "对话命名问题");

  const header = read("components/session/TaskSessionGroupHeader.tsx");
  assert.match(header, /taskSessionGroupHeading\(/, "分组头必须走同一套文案函数");
  assert.match(header, /fontSize: "13px"/, "分组头与普通会话行同名号");
  assert.match(header, /fontWeight: 500/, "字重与未选中的会话行一致");
  assert.doesNotMatch(header, /count/, "分组头不再渲染成员计数");
  assert.match(header, /data-task-group=/, "分组头的定位属性保持不变");
});

test("成员名剥掉分组头的共享前缀，保留区分点", () => {
  assert.equal(taskSessionMemberLabel("对话命名问题 · 把任务改成对话式", "对话命名问题"), "把任务改成对话式");
  assert.equal(taskSessionMemberLabel("对话命名问题 / #7 · 把任务改成对话式", "对话命名问题"), "#7 · 把任务改成对话式");
  assert.equal(taskSessionMemberLabel("对话命名问题", "对话命名问题"), "对话命名问题", "只剩任务名时原样返回，不渲染空名");
  assert.equal(taskSessionMemberLabel("改过名的一条", "对话命名问题"), "改过名的一条", "改过名的会话不按前缀硬切");
  assert.equal(taskSessionMemberLabel("对话命名问题续篇 · 另一轮", "对话命名问题"), "对话命名问题续篇 · 另一轮", "前缀必须在词边界上");
  assert.equal(taskSessionMemberLabel("任务 A · 一", ""), "任务 A · 一", "没有分组头时不做任何裁剪");
});

test("两条侧栏都渲染分组头，并把 nested 交给会话卡", () => {
  const single = read("components/session/SessionList.tsx");
  const multi = read("components/session/MultiProjectSessionList.tsx");
  for (const [name, source] of [["SessionList", single], ["MultiProjectSessionList", multi]]) {
    assert.match(source, /row\.type === "task-group"/, `${name} must branch on task-group rows`);
    assert.match(source, /<TaskSessionGroupHeader/, `${name} must render the group header`);
    assert.match(source, /nested=\{row\.nested\}/, `${name} must indent grouped sessions`);
    assert.match(source, /stripNamePrefix=\{row\.stripNamePrefix\}/, `${name} must strip the shared task prefix`);
  }
  // 前缀只在渲染时剥：名字本身（重命名草稿、落库值）一个字都不能动。
  const card = read("components/session/SessionCard.tsx");
  assert.match(card, /stripNamePrefix \? taskSessionMemberLabel\(displayName, stripNamePrefix\)/, "SessionCard must apply the prefix stripping");
  assert.match(card, /title=\{shownName\}/, "hover title must show the stripped name too");
  assert.match(card, /setDraftName\(name\)/, "rename draft must start from the stored name");
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
