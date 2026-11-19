import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import React from "react";
import { renderToString } from "react-dom/server";
import { I18nProvider } from "../src/i18n/index.tsx";
import { ConversationMessages } from "../src/components/task/ConversationView.tsx";
import { visibleConversationActivities } from "../src/shared/activityModel.ts";

// #88 现场形状：整轮跑完、末端 gate 等待审批。当时同屏出现两张冲突的卡 ——
// 占位回复「验收 / 等待你处理」和审批卡并存，用户分不清哪个才是要处理的对象。
// 这里按真实数据形状钉住修复：等待审批时只留审批卡，占位回复不冒充结果。

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

const base = {
  task_id: "task_88",
  author: "agent",
  workflow_choice: "empty",
  created_at: "2026-11-19T08:14:23Z",
};

// task_activities 真实行：占位回复由结算写成 waiting_user + summary=gate prompt。
const userMessage = {
  ...base,
  id: "act_6845c9b3",
  type: "user_message",
  author: "user",
  role: "user",
  body: "目标：把「worktree 收尾」完整做成 MindFS 原生能力",
};

const placeholderReply = {
  ...base,
  id: "act_bc785bf0",
  type: "agent_reply",
  role: "agent",
  body: "",
  status: "waiting_user",
  summary: "验收",
  purpose: "reply",
  run_id: "run-d3ef85eed889c18f",
  reply_to_id: "act_6845c9b3",
};

const waitingGate = {
  ...base,
  id: "wf:run-d3ef85eed889c18f:verify::0:1:human_waiting",
  type: "human_gate",
  author: "system",
  role: "system",
  body: "等待你确认：验收",
  status: "waiting_user",
  run_id: "run-d3ef85eed889c18f",
  node_run_id: "run-d3ef85eed889c18f:verify::0:1",
  payload_json: JSON.stringify({
    prompt: "验收",
    node_id: "verify",
    node_name: "验收",
    node_run_id: "run-d3ef85eed889c18f:verify::0:1",
    upstream: [{ node_id: "implement", name: "代码实现", summary: "已补齐 B4 并跑完门禁。" }],
  }),
};

const progressReport = {
  ...base,
  id: "act_01bc6274",
  type: "agent_reply",
  role: "agent",
  body: "已用「新功能开发」流程开始。",
  status: "completed",
  purpose: "progress",
  reply_to_id: "act_6845c9b3",
};

const render = (activities) =>
  renderToString(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(ConversationMessages, { activities }),
    ),
  );

test("#88 形状：等待审批时占位回复不显示，只留审批卡", () => {
  const visible = visibleConversationActivities([userMessage, placeholderReply, waitingGate, progressReport]);
  assert.deepEqual(
    visible.map((a) => a.id),
    [userMessage.id, waitingGate.id, progressReport.id],
    "the waiting placeholder reply is suppressed, everything else survives",
  );
  const html = render(visible);
  assert.match(html, /data-testid="task-human-gate"/, "the gate card is the approval surface");
  assert.doesNotMatch(html, /Waiting for you/, "no leftover 等待你处理 chip");
});

test("审批卡在等待态写「待你审批」，不是泛化的「等待你处理」", () => {
  const html = render([userMessage, waitingGate]);
  assert.match(html, /待你审批|Awaiting your approval/);
  assert.doesNotMatch(html, /Waiting for your approval/, "old copy is gone");
});

test("没有 gate 时等待中的占位回复照常显示", () => {
  // condition 求值失败等场景同样停 waiting_user，但没有审批卡可看：那行是唯一说明。
  const visible = visibleConversationActivities([userMessage, placeholderReply]);
  assert.deepEqual(visible.map((a) => a.id), [userMessage.id, placeholderReply.id]);
});

test("gate 结算后占位回复回到结果行", () => {
  const settledGate = {
    ...waitingGate,
    status: "completed",
    payload_json: JSON.stringify({ ...JSON.parse(waitingGate.payload_json), decision: "approve", status: "success" }),
  };
  const settledReply = { ...placeholderReply, status: "completed", summary: "验收通过，已收尾。" };
  const visible = visibleConversationActivities([userMessage, settledReply, settledGate]);
  assert.deepEqual(visible.map((a) => a.id), [userMessage.id, settledReply.id, settledGate.id]);
});

test("只抑制同一 Run 的占位行，别的 Run 不受影响", () => {
  const otherReply = { ...placeholderReply, id: "other", run_id: "run-other" };
  const visible = visibleConversationActivities([waitingGate, otherReply]);
  assert.deepEqual(visible.map((a) => a.id), [waitingGate.id, otherReply.id]);
});

test("ConversationMessages 过滤走同一个函数，不是另写一套", () => {
  const view = read("../src/components/task/ConversationView.tsx");
  assert.match(view, /visibleConversationActivities\(activities\)/);
});
