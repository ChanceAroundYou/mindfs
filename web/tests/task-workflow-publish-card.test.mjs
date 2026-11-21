import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import React from "react";
import { renderToString } from "react-dom/server";
import { I18nProvider } from "../src/i18n/index.tsx";
import { TaskWorkflowPublishCard } from "../src/components/task/TaskWorkflowPublishCard.tsx";
import { activityTypeTagKey } from "../src/shared/activityModel.ts";

// W3 T2：Router 想把一张 workflow 固化进目录时，任务详情里落一张待批准卡。
// 这些用例钉住「批准是显式动作」这条边界：卡片必须说清批什么、作用域，以及
// 决定只能走 /workflow-publish（普通留言不能批）。

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

const card = {
  id: "workflow-publish:dec-1",
  task_id: "t1",
  type: "workflow_publish",
  author: "router",
  role: "system",
  body: "Router 想把一版工作流固化进目录：**代码复查**（`wf-review`，全局（所有项目），2 个节点）。",
  workflow_choice: "empty",
  status: "waiting_user",
  created_at: "2026-11-21T10:00:00Z",
  payload_json: JSON.stringify({
    request: { workflow_id: "wf-review", name: "代码复查", scope: "global" },
    decision_id: "dec-1",
  }),
};

const renderCard = (props) =>
  renderToString(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(TaskWorkflowPublishCard, { activity: card, onDecision: () => {}, ...props }),
    ),
  );

test("待批准卡说清批什么：名字、全局作用域、正文与两个按钮", () => {
  const html = renderCard({});
  assert.match(html, /data-testid="task-workflow-publish"/);
  assert.match(html, /代码复查/, "workflow name is shown");
  assert.match(html, /全局/, "global scope is spelled out");
  assert.match(html, /wf-review/, "catalog id is visible");
  assert.match(html, /Approve/);
  assert.match(html, /Reject/);
});

test("已决定的卡不再给按钮，只留结论", () => {
  const approved = { ...card, status: "completed", body: "已批准：工作流「代码复查」以全局版本 wf1:v2 发布。" };
  const html = renderCard({ activity: approved });
  assert.doesNotMatch(html, /<button/, "no buttons after decision");
  assert.match(html, /Approved and published/);

  const rejected = { ...card, status: "cancelled", body: "已驳回：这一版工作流没有写入目录。" };
  const rejectedHtml = renderCard({ activity: rejected });
  assert.doesNotMatch(rejectedHtml, /<button/);
  assert.match(rejectedHtml, /Rejected/);
});

test("类型胶囊有专属文案，不与审批卡混用", () => {
  assert.equal(
    activityTypeTagKey({ type: "workflow_publish", role: "system", status: "waiting_user" }),
    "task.activity.type.workflowPublish",
  );
  const zh = read("../src/i18n/locales/zh-CN-tasks.ts");
  const en = read("../src/i18n/locales/en-US-tasks.ts");
  assert.match(zh, /"task\.activity\.publishWaiting"/);
  assert.match(en, /"task\.activity\.publishWaiting"/);
});

test("对话流按类型把 workflow_publish 交给这张卡", () => {
  const src = read("../src/components/task/ActivityMessage.tsx");
  assert.match(src, /activity\.type === "workflow_publish"/);
  assert.match(src, /TaskWorkflowPublishCard/);
});

test("决定只走 /workflow-publish，空附言整键省略", () => {
  const src = read("../src/services/task/workflowPublish.ts");
  assert.match(src, /\/api\/tasks\/\$\{encodeURIComponent\(taskId\)\}\/workflow-publish/);
  assert.match(src, /trimmedNote \? \{ note: trimmedNote \} : \{\}/);
  assert.doesNotMatch(src, /\/comments/, "publish must never ride the comment endpoint");
});

test("hook 暴露 decideWorkflowPublish 并原地替换回包行", () => {
  const src = read("../src/hooks/useTaskActivities.ts");
  assert.match(src, /decideWorkflowPublish: \(/);
  assert.match(src, /postTaskWorkflowPublish\(/);
  assert.match(src, /entry\.id === updated\.id \? updated : entry/);
});
