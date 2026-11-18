import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import React from "react";
import { renderToString } from "react-dom/server";
import { I18nProvider } from "../src/i18n/index.tsx";
import { ConversationMessages } from "../src/components/task/ConversationView.tsx";
import { conversationRows } from "../src/shared/activityModel.ts";

// 同一留言下的「结果重复展示」（R1 T10 复核 §4 的 UX 残留）。
//
// 引擎结算会把节点结果回填到占位回复的 summary（保证路径），Router 的运行期事件
// 回合再 say 一条 purpose=progress 的汇报（尽力路径，带失败节点/未验证项等增量
// 信息）。两条都不删，但默认不能把结果句给用户看两遍：结算行留在原地，同一留言
// 下的 progress 行折进它后面的「运行汇报 N 条」里，点开仍能看到原文。

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

const base = {
  task_id: "task-1",
  author: "agent",
  workflow_choice: "empty",
  created_at: "2026-11-19T10:00:00Z",
};

const userMessage = {
  ...base,
  id: "u1",
  type: "user_message",
  author: "user",
  role: "user",
  body: "跑一下 T10 快速确认",
};

const settledReply = {
  ...base,
  id: "r1",
  type: "agent_reply",
  role: "agent",
  body: "",
  status: "completed",
  summary: "节点结果：快速验证已完成。",
  purpose: "reply",
  run_id: "run-1",
  reply_to_id: "u1",
};

const progressStarted = {
  ...base,
  id: "p1",
  type: "agent_reply",
  role: "agent",
  body: "已启动「T10 快速确认」流程。",
  status: "completed",
  purpose: "progress",
  run_id: "run-1",
  reply_to_id: "u1",
};

const progressReport = {
  ...base,
  id: "p2",
  type: "agent_reply",
  role: "agent",
  body: "事件汇报：节点已完成，但没有实际验证支撑。",
  status: "completed",
  purpose: "progress",
  reply_to_id: "u1",
};

const rowIds = (rows) =>
  rows.map((row) => (row.kind === "activity" ? row.activity.id : `reports:${row.parentId}`));

const render = (activities) =>
  renderToString(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(ConversationMessages, { activities }),
    ),
  );

test("同一条留言的 progress 汇报折进结算回复后的分组", () => {
  const rows = conversationRows([userMessage, settledReply, progressStarted, progressReport]);
  assert.deepEqual(rowIds(rows), ["u1", "r1", "reports:r1"]);
  assert.deepEqual(rows[2].reports.map((a) => a.id), ["p1", "p2"], "report order is preserved");
});

test("结算行还没落地时 progress 必须保持可见", () => {
  // 运行中：用户正是靠这行知道流程已经跑起来了，折叠它等于把进度藏掉。
  const running = { ...settledReply, status: "running", summary: "" };
  const rows = conversationRows([userMessage, running, progressStarted]);
  assert.deepEqual(rowIds(rows), ["u1", "r1", "p1"]);
});

test("没有归属或归属到未结算留言的 progress 不折叠", () => {
  const orphan = { ...progressReport, id: "p-orphan", reply_to_id: "" };
  assert.deepEqual(rowIds(conversationRows([userMessage, orphan])), ["u1", "p-orphan"]);

  // 折叠按 reply_to_id 找父行，不是「最近的结算行」：u2 之后的汇报仍回到 r1 组。
  const otherUser = { ...userMessage, id: "u2", body: "另一个需求" };
  assert.deepEqual(
    rowIds(conversationRows([userMessage, settledReply, otherUser, progressReport])),
    ["u1", "r1", "reports:r1", "u2"],
  );
});

test("非 progress 的回复不会被吞进分组", () => {
  const secondReply = { ...settledReply, id: "r2", summary: "第二条结果", purpose: "result" };
  assert.deepEqual(
    rowIds(conversationRows([userMessage, settledReply, progressReport, secondReply])),
    ["u1", "r1", "reports:r1", "r2"],
  );
});

test("失败/等待用户的结算行同样算父行", () => {
  // 运行失败时结算行 status=failed（summary 写失败原因），汇报仍要折进去，
  // 否则用户会看到同一轮失败被说两遍。
  const failed = { ...settledReply, status: "failed", summary: "节点失败：超时" };
  assert.deepEqual(
    rowIds(conversationRows([userMessage, failed, progressReport])),
    ["u1", "r1", "reports:r1"],
  );
});

test("多轮留言时各自折到自己的结算行", () => {
  const secondUser = { ...userMessage, id: "u2", body: "第二轮" };
  const secondSettled = { ...settledReply, id: "r2", summary: "第二轮结果", reply_to_id: "u2" };
  const secondReport = { ...progressReport, id: "p3", reply_to_id: "u2" };
  assert.deepEqual(
    rowIds(
      conversationRows([
        userMessage,
        settledReply,
        progressReport,
        secondUser,
        secondSettled,
        secondReport,
      ]),
    ),
    ["u1", "r1", "reports:r1", "u2", "r2", "reports:r2"],
  );
});

test("对话流默认折叠：结果句只出现一次，汇报正文与计数可见", () => {
  const html = render([userMessage, settledReply, progressStarted, progressReport]);
  assert.match(html, /节点结果：快速验证已完成。/, "settled summary stays visible");
  assert.match(html, /data-testid="activity-reports-toggle"/, "collapsed group exposes its toggle");
  // 语言随运行环境（node 的 navigator.language 是 en-US），两种文案都算数。
  assert.match(html, /运行汇报 2 条|Run reports \(2\)/, "toggle names how many reports are folded");
  assert.doesNotMatch(html, /已启动「T10 快速确认」流程。/, "folded progress body is hidden by default");
  assert.doesNotMatch(html, /没有实际验证支撑/, "folded report body is hidden by default");
});

test("折叠组件可展开且复用同一条消息渲染", () => {
  const group = read("../src/components/task/ActivityReportsGroup.tsx");
  assert.match(group, /useState/, "the group keeps its own open/closed state");
  assert.match(group, /<ActivityMessage/, "expanded reports reuse ActivityMessage, not a second renderer");
  assert.match(group, /data-testid="activity-reports"/, "expanded group is identifiable");
});

test("i18n 键齐全", () => {
  const zh = read("../src/i18n/locales/zh-CN-tasks.ts");
  const en = read("../src/i18n/locales/en-US-tasks.ts");
  assert.ok(zh.includes('"task.activity.reports"'), "zh-CN must define task.activity.reports");
  assert.ok(en.includes('"task.activity.reports"'), "en-US must define task.activity.reports");
});
