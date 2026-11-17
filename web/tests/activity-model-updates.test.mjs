import assert from "node:assert/strict";
import { test } from "vitest";
import { applyActivityUpdated } from "../src/shared/activityModel.ts";

// ── 活动流增量合并：WS task.activity.updated ───────────────────────────────
// 从 conversation-view.test.mjs 拆出：那个文件已顶到 500 行硬线。
const baseActivity = {
  id: "a1",
  task_id: "task-1",
  type: "agent_reply",
  author: "agent",
  role: "agent",
  body: "正在准备执行工作流...",
  workflow_choice: "empty",
  created_at: "2026-11-12T08:00:00Z",
};

test("applyActivityUpdated merges status/summary into the matching activity only", () => {
  const other = { ...baseActivity, id: "a2" };
  const list = [baseActivity, other];
  const next = applyActivityUpdated(list, "a1", { status: "completed", summary: "done" });

  assert.equal(next[0].status, "completed");
  assert.equal(next[0].summary, "done");
  assert.equal(next[0].body, baseActivity.body);
  assert.equal(next[1], other);
});

test("applyActivityUpdated returns the same array when the id is unknown", () => {
  const list = [baseActivity];
  assert.equal(applyActivityUpdated(list, "missing", { status: "failed" }), list);
});

test("applyActivityUpdated removes the activity when the server hides it", () => {
  const other = { ...baseActivity, id: "a2" };
  const list = [baseActivity, other];

  const hidden = applyActivityUpdated(list, "a1", { hidden: true });
  assert.deepEqual(hidden.map((activity) => activity.id), ["a2"]);
  assert.equal(hidden[0], other);

  // 非 hidden 的更新仍照常合并，不能把 hidden 字段的缺席当成删除。
  const visible = applyActivityUpdated(list, "a1", { hidden: false, status: "running" });
  assert.equal(visible.length, 2);
  assert.equal(visible[0].status, "running");
});