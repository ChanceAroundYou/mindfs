import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { test } from "vitest";
import React from "react";
import { renderToString } from "react-dom/server";
import { I18nProvider } from "../src/i18n/index.tsx";
import { ConversationMessages } from "../src/components/task/ConversationView.tsx";

// 运行汇报不再折叠（2026-11-19 用户裁定：「1.运行汇报为什么要加折叠功能？」）。
//
// 之前的实现把同一留言下 purpose=progress 的汇报折进结算回复后面的一个默认
// 收起的分组里，理由是结果句重复。代价是用户看不到流程到底跑到哪一步，
// 还得先点开才知道有没有上报。现在每行按时间顺序直接展示，只加一个浅色
// 标记，让运行中的进度与结算回复在视觉上仍能区分。

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

const render = (activities) =>
  renderToString(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(ConversationMessages, { activities }),
    ),
  );

test("运行汇报按时间顺序原样渲染，不折叠也不隐藏", () => {
  const html = render([userMessage, settledReply, progressStarted, progressReport]);
  assert.match(html, /data-testid="activity-agent"/, "agent replies render inline");
  assert.match(html, /已启动「T10 快速确认」流程。/, "progress body is visible without expanding");
  assert.match(html, /没有实际验证支撑/, "every progress report is visible");
  assert.match(html, /节点结果：快速验证已完成。/, "settled summary stays visible");
  assert.doesNotMatch(html, /activity-reports-toggle/, "no expand/collapse control remains");
  assert.doesNotMatch(html, /activity-reports-group/, "no folded group container remains");
});

test("progress 行按到达顺序排列在结算行之前", () => {
  const html = render([userMessage, progressStarted, progressReport, settledReply]);
  const startedAt = html.indexOf("已启动「T10 快速确认」流程。");
  const settledAt = html.indexOf("节点结果：快速验证已完成。");
  assert.ok(startedAt >= 0 && settledAt >= 0, "both rows are rendered");
  assert.ok(startedAt < settledAt, "rendering follows activity order, not a regrouped order");
});

test("只有 purpose=progress 的行带「进度」标记", () => {
  const html = render([userMessage, settledReply, progressStarted]);
  assert.match(html, /data-testid="activity-report-tag"/, "progress row carries the tag");
  assert.match(html, /进度|Progress/, "tag is localised");
  // 结算回复与用户留言都不该被标记成进度。
  const tagCount = (html.match(/data-testid="activity-report-tag"/g) || []).length;
  assert.equal(tagCount, 1, "exactly one progress row is tagged");
});

test("用户留言与其他角色不受影响", () => {
  const html = render([userMessage, progressStarted]);
  assert.match(html, /data-testid="activity-user"/, "user bubble still renders");
  assert.match(html, /跑一下 T10 快速确认/, "user body intact");
});

test("折叠组件与行重组函数已删除", () => {
  assert.equal(
    existsSync(new URL("../src/components/task/ActivityReportsGroup.tsx", import.meta.url)),
    false,
    "ActivityReportsGroup must be gone, not merely unused",
  );
  const model = read("../src/shared/activityModel.ts");
  assert.doesNotMatch(model, /conversationRows/, "no conversationRows regrouping helper");
  assert.doesNotMatch(model, /isSettledReply/, "no settled-reply detection for folding");
});

test("折叠专用 i18n 键已删除，标记键已就位", () => {
  const zh = read("../src/i18n/locales/zh-CN-tasks.ts");
  const en = read("../src/i18n/locales/en-US-tasks.ts");
  for (const source of [zh, en]) {
    assert.doesNotMatch(source, /"task\.activity\.reports"/, "folded-group count key is gone");
    assert.doesNotMatch(source, /"task\.activity\.reportsHide"/, "folded-group toggle key is gone");
    assert.match(source, /"task\.activity\.reportTag"/, "inline tag key is defined");
  }
});
