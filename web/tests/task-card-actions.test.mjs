import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import {
  TASK_ACTION_BUTTON_META,
  taskActionButtons,
  taskCardActions,
} from "../src/components/task/taskCardActions.ts";

// 看板任务卡与任务详情 header 「此刻真正可做的动作」只有一份定义
// （taskCardActions.ts）。
//
// 2026-11-22 用户要求两处完全统一（样式、颜色、数量、种类、hover 文案），
// 于是 `taskActionButtons` 成了**两处共读**的那一份：键序、文案、配色都从它和
// TASK_ACTION_BUTTON_META 取。旧的 taskDetailActions.ts（详情另立一套口径）已删除。
// 这里既钉纯函数本身，也钉「两处都读同一份」这条结构约束。

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

test("未开始：开始 + 取消（还没有产出可交付，所以没有「完成」）", () => {
  assert.deepEqual(taskCardActions(task({ status: "pending" })), ["start", "cancel"]);
});

test("待处理：完成 + 取消", () => {
  assert.deepEqual(taskCardActions(task({ status: "waiting_user" })), ["complete", "cancel"]);
});

test("暂停后：恢复 + 完成 + 取消", () => {
  assert.deepEqual(taskCardActions(task({ status: "paused" })), ["resume", "complete", "cancel"]);
});

test("运行中：只有终止（它会真停会话与工作流，落回待处理）", () => {
  // 暂停会让「活还在后台跑、界面却说停了」，完成/取消也不打断会话；2026-11-22
  // 用户裁定运行中只留终止。
  assert.deepEqual(taskCardActions(task({ status: "running" })), ["terminate"]);
});

test("有活着的 worktree 时，未结束的任务先给「收尾」", () => {
  assert.deepEqual(taskCardActions(withWorktree({ status: "waiting_user" })), ["finish-worktree", "complete", "cancel"]);
  // 运行中不摆收尾：树是这一轮正在写的工作区，唯一动作是终止。
  assert.deepEqual(taskCardActions(withWorktree({ status: "running" })), ["terminate"]);
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

test("已归档：返回 + 取消归档（收尾收起）", () => {
  // 2026-11-22 用户裁定「已归档以卡片为准，卡片补『返回』」：
  // 归档卡要先能拿回活动列，所以返回与取消归档并列。
  assert.deepEqual(
    taskCardActions(task({ status: "success", archived_at: "2026-11-20T00:00:00Z" })),
    ["reopen", "unarchive-task"],
  );
  assert.deepEqual(
    taskCardActions(withWorktree({ status: "success", archived_at: "2026-11-20T00:00:00Z" })),
    ["reopen", "unarchive-task"],
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

test("删除不进共享动作表，只由 taskActionButtons 给终态追加在最右", () => {
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
  // 非终态没有删除入口；三种终态形状都在末尾补一个删除。
  assert.deepEqual(taskActionButtons(task({ status: "running" })), ["terminate"]);
  assert.deepEqual(taskActionButtons(task({ status: "pending" })), ["start", "cancel"]);
  assert.deepEqual(taskActionButtons(task({ status: "success" })), ["reopen", "archive-task", "delete-task"]);
  assert.deepEqual(
    taskActionButtons(withWorktree({ status: "success" })),
    ["finish-worktree", "reopen", "archive-task", "delete-task"],
  );
  assert.deepEqual(
    taskActionButtons(task({ status: "success", archived_at: "2026-11-20T00:00:00Z" })),
    ["reopen", "unarchive-task", "delete-task"],
  );
});

test("每个动作都有文案键与档位，且两种语言都写了", () => {
  const zh = readFileSync(new URL("../src/i18n/locales/zh-CN-tasks.ts", import.meta.url), "utf8");
  const en = readFileSync(new URL("../src/i18n/locales/en-US-tasks.ts", import.meta.url), "utf8");
  const keys = Object.keys(TASK_ACTION_BUTTON_META);
  assert.equal(keys.length, 11, "the shared table covers every button, delete included");
  for (const [action, meta] of Object.entries(TASK_ACTION_BUTTON_META)) {
    assert.match(meta.labelKey, /^task\./, `${action}: labels come from the task namespace`);
    assert.ok(["default", "primary", "success", "warn"].includes(meta.tone), `${action}: known tone`);
    assert.match(zh, new RegExp(`"${meta.labelKey}": `), `${action}: zh label missing`);
    assert.match(en, new RegExp(`"${meta.labelKey}": `), `${action}: en label missing`);
  }
});

test("卡片与详情 header 读同一份动作表、同一套图形与配色", () => {
  const card = readFileSync(new URL("../src/components/task/TaskCardRows.tsx", import.meta.url), "utf8");
  const panel = readFileSync(new URL("../src/components/task/TaskDetailPanel.tsx", import.meta.url), "utf8");
  for (const [name, source] of [["card", card], ["detail", panel]]) {
    assert.match(source, /taskActionButtons\(task\)/, `${name}: action keys come from the shared list`);
    assert.match(source, /TASK_ACTION_BUTTON_META\[key\]/, `${name}: labels/tones come from the shared table`);
    assert.match(source, /taskActionIcons\[key\]/, `${name}: icons come from the shared map`);
    assert.match(source, /taskActionIconStyle\(/, `${name}: tone → colours go through one mapping`);
    // 旧的互斥条件不许回来：两处都不许再自己算「能不能完成 / 能不能重开」。
    assert.doesNotMatch(source, /canCompleteTask|canReopenTask/, name);
  }
  // 详情侧不许再出现自己的一套动作表。
  assert.doesNotMatch(panel, /taskDetailActions|TASK_DETAIL_ACTION_META/);
});

console.log("task-card-actions.test.mjs: OK");
