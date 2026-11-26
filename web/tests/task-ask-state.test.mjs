// 提问卡的状态必须一眼可见，且 Router 卡与会话卡共用同一套视觉语言。
//
// 症状（2026-11-27）：提问卡答完之后仍然保持红色「等你选择」的外观；被回合结束
// 或新提问取代收口的卡和「还没答」的卡长得一样，看板上还挂着已失效的等待徽标。
// 这里钉住三种状态的渲染：等待=waiting、已答复=answered（附答案摘要）、
// 已关闭=closed（附收口原因）。

import assert from "node:assert/strict";
import React from "react";
import { renderToString } from "react-dom/server";
import { test } from "vitest";
import { I18nProvider } from "../src/i18n/index.tsx";
import { ActivityMessage } from "../src/components/task/ActivityMessage.tsx";

const h = React.createElement;
const render = (activity) =>
  renderToString(h(I18nProvider, null, h(ActivityMessage, { activity })));

const baseActivity = {
  id: "act-ask-state",
  task_id: "task-1",
  type: "question",
  role: "agent",
  workflow_choice: "empty",
  created_at: "2026-11-27T08:00:00Z",
  session_key: "",
  tool_use_id: "",
  body: "先做哪个？",
};

const questionPayload = (extra = {}) =>
  JSON.stringify({
    source: "router",
    questions: [
      { question: "先做哪个？", header: "需要你拍板", options: [{ label: "A" }, { label: "B" }] },
    ],
    ...extra,
  });

test("等待回答的提问卡标 waiting", () => {
  const html = render({ ...baseActivity, status: "waiting_user", payload_json: questionPayload() });
  assert.match(html, /data-testid="ask-user-state" data-state="waiting"/);
});

test("已答复的提问卡标 answered 并显示答案摘要", () => {
  const html = render({
    ...baseActivity,
    status: "completed",
    payload_json: questionPayload({ answers: { q_0: "A" } }),
  });
  assert.match(html, /data-testid="ask-user-state" data-state="answered"/);
  assert.match(html, /data-testid="ask-user-answer-summary"/);
  assert.match(html, /(你的答复：A|Your answer: A)/);
});

test("被收口的提问卡标 closed 并显示收口原因", () => {
  const html = render({
    ...baseActivity,
    status: "cancelled",
    summary: "回合已结束，提问未回答",
    payload_json: questionPayload(),
  });
  assert.match(html, /data-testid="ask-user-state" data-state="closed"/);
  assert.match(html, /data-testid="ask-user-status-note"/);
  assert.match(html, /回合已结束，提问未回答/);
});