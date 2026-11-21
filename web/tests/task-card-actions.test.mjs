import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import { TASK_CARD_ACTION_META, taskCardActions } from "../src/components/task/taskCardActions.ts";

// 任务卡「此刻真正可做的动作」只有一份定义（taskCardActions.ts）。
//
// 看板卡片的按钮行与任务详情 header 的「操作控件」菜单都读它 —— 以前两边各抄
// 一遍条件，详情菜单那三段时间互斥，用户实测「卡片上有好几个动作、菜单只显示一个」。
// 这里钉的是纯函数本身的行为；接线由 task-detail-header / task-card-parity 的源码
// 契约钉住（两边都必须 import 这一份）。

const task = (extra = {}) => ({
  status: "waiting_user",
  create_worktree: false,
  ...extra,
});

const withWorktree = (extra = {}) => task({
  create_worktree: true,
  worktree_path: "/x/.worktree/t1",
  ...extra,
});

test("未结束、没有 worktree：完成 + 取消", () => {
  for (const status of ["pending", "waiting_user"]) {
    assert.deepEqual(taskCardActions(task({ status })), ["complete", "cancel"], status);
  }
});

test("暂停后：恢复 + 完成 + 取消", () => {
  assert.deepEqual(taskCardActions(task({ status: "paused" })), ["resume", "complete", "cancel"]);
});

test("正在跑：暂停 + 取消，但没有「完成」（活还在动）", () => {
  assert.deepEqual(taskCardActions(task({ status: "running" })), ["pause", "cancel"]);
});

test("有活着的 worktree 时，未结束的任务先给「收尾」", () => {
  assert.deepEqual(taskCardActions(withWorktree({ status: "waiting_user" })), ["finish-worktree", "complete", "cancel"]);
  assert.deepEqual(taskCardActions(withWorktree({ status: "running" })), ["finish-worktree", "pause", "cancel"]);
});

test("终态：重新打开 + 归档；worktree 还在就再给收尾", () => {
  for (const status of ["success", "fail", "cancelled"]) {
    assert.deepEqual(taskCardActions(task({ status })), ["reopen", "archive-task"], status);
    assert.deepEqual(
      taskCardActions(withWorktree({ status })),
      ["finish-worktree", "reopen", "archive-task"],
      `${status}: a live worktree is exactly what finish is for`,
    );
  }
});

test("已归档：只剩取消归档（收尾与重开一起收起）", () => {
  assert.deepEqual(taskCardActions(task({ status: "success", archived_at: "2026-11-20T00:00:00Z" })), ["unarchive-task"]);
  assert.deepEqual(
    taskCardActions(withWorktree({ status: "success", archived_at: "2026-11-20T00:00:00Z" })),
    ["unarchive-task"],
    "归档语义是「这条已经翻篇」，不再摆收尾键",
  );
});

test("worktree 目录已删或从未建过：不给收尾", () => {
  assert.deepEqual(
    taskCardActions(withWorktree({ status: "waiting_user", worktree_missing: true })),
    ["complete", "cancel"],
    "nothing left to tear down",
  );
  assert.deepEqual(
    taskCardActions(task({ status: "waiting_user", worktree_path: "/x" })),
    ["complete", "cancel"],
    "create_worktree 才是「用不用 worktree」的开关",
  );
});

test("删除永远不在共享动作表里（两处都单独摆）", () => {
  const samples = [
    task(),
    task({ status: "running" }),
    task({ status: "paused" }),
    withWorktree(),
    task({ status: "success" }),
    withWorktree({ status: "success" }),
    task({ status: "cancelled", archived_at: "2026-11-20T00:00:00Z" }),
  ];
  for (const sample of samples) {
    assert.ok(!taskCardActions(sample).includes("delete-task"), JSON.stringify(sample));
  }
});

test("每个动作都有文案键与档位，且两种语言都写了", () => {
  const zh = readFileSync(new URL("../src/i18n/locales/zh-CN-tasks.ts", import.meta.url), "utf8");
  const en = readFileSync(new URL("../src/i18n/locales/en-US-tasks.ts", import.meta.url), "utf8");
  const keys = Object.keys(TASK_CARD_ACTION_META);
  assert.equal(keys.length, 8, "the shared table covers every action except delete");
  for (const [action, meta] of Object.entries(TASK_CARD_ACTION_META)) {
    assert.match(meta.labelKey, /^task\./, `${action}: labels come from the task namespace`);
    assert.ok(["default", "primary", "warn"].includes(meta.tone), `${action}: known tone`);
    assert.match(zh, new RegExp(`"${meta.labelKey}": `), `${action}: zh label missing`);
    assert.match(en, new RegExp(`"${meta.labelKey}": `), `${action}: en label missing`);
  }
});

test("卡片与详情菜单都从这一份取动作，不再各写一套条件", () => {
  const card = readFileSync(new URL("../src/components/task/TaskCardRows.tsx", import.meta.url), "utf8");
  const panel = readFileSync(new URL("../src/components/task/TaskDetailPanel.tsx", import.meta.url), "utf8");
  assert.match(card, /import \{ taskCardActions \} from "\.\/taskCardActions";/);
  assert.match(panel, /import \{ TASK_CARD_ACTION_META, taskCardActions, type TaskCardActionKey \} from "\.\/taskCardActions";/);
  assert.match(card, /const actions = taskCardActions\(task\);/);
  assert.match(panel, /taskCardActions\(task\)\.map/);
  // 旧的互斥条件不许回来。
  assert.doesNotMatch(panel, /if \(canCompleteTask\)/);
  assert.doesNotMatch(panel, /if \(canReopenTask\)/);
});

console.log("task-card-actions.test.mjs: OK");