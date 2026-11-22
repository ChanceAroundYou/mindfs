import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import { TASK_CARD_ACTION_META, taskCardActions } from "../src/components/task/taskCardActions.ts";

// 看板任务卡「此刻真正可做的动作」只有一份定义（taskCardActions.ts）。
//
// 卡片按钮行读它。任务详情 header 自 2026-11-22 起改用另一份口径
// （taskDetailActions.ts：平铺、含删除、归档可返回、待开始给开始），两者**有意
// 不同**，差异写在 taskDetailActions.ts 的文件头。这里只钉卡片这份纯函数本身。

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

test("看板卡片从这一份取动作；详情面板用自己的口径（两者有意不同）", () => {
  const card = readFileSync(new URL("../src/components/task/TaskCardRows.tsx", import.meta.url), "utf8");
  const panel = readFileSync(new URL("../src/components/task/TaskDetailPanel.tsx", import.meta.url), "utf8");
  assert.match(card, /import \{ taskCardActions \} from "\.\/taskCardActions";/);
  assert.match(card, /const actions = taskCardActions\(task\);/);
  // 2026-11-22 用户把详情 header 改成平铺按钮，口径与卡片不同（归档可返回、
  // 待开始给开始、执行中只给中断…），详情侧不许再读卡片这份表。
  assert.match(panel, /taskDetailActions\(task\)\.map/);
  assert.doesNotMatch(panel, /taskCardActions\(task\)/);
  // 旧的互斥条件不许回来。
  assert.doesNotMatch(panel, /if \(canCompleteTask\)/);
  assert.doesNotMatch(panel, /if \(canReopenTask\)/);
});

console.log("task-card-actions.test.mjs: OK");