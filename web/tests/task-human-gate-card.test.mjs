import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import React from "react";
import { renderToString } from "react-dom/server";
import { I18nProvider } from "../src/i18n/index.tsx";
import { TaskHumanGateCard } from "../src/components/task/TaskHumanGateCard.tsx";

// 审批卡可读性（2026-11-19 用户裁定）：
// - 卡片必须说明「在批什么」：节点名 + 审批提示 + 上游产出摘要；
// - 卡片内加「附言」输入，随批准一起交给 Router 驱动下一轮；普通输入框仍禁用。

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

const gate = {
  id: "g1",
  task_id: "t1",
  type: "human_gate",
  author: "system",
  role: "system",
  body: "等待你确认：确认可以上线",
  workflow_choice: "empty",
  status: "waiting_user",
  created_at: "2026-11-19T10:00:00Z",
  payload_json: JSON.stringify({
    prompt: "确认可以上线",
    node_id: "verify",
    node_name: "验收",
    node_run_id: "run-1:verify::0:1",
    upstream: [
      { node_id: "implement", name: "代码实现", summary: "已实现登录页并补了回归测试。" },
    ],
  }),
};

const renderCard = (props) =>
  renderToString(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(TaskHumanGateCard, { activity: gate, onDecision: () => {}, ...props }),
    ),
  );

test("审批卡标题、节点名与审批提示都在", () => {
  const html = renderCard({});
  assert.match(html, /验收/, "gate node name is shown");
  assert.match(html, /确认可以上线/, "gate prompt is shown");
  assert.match(html, /data-testid="task-human-gate"/);
});

test("审批卡展示上游节点产出摘要", () => {
  const html = renderCard({});
  assert.match(html, /data-testid="task-human-gate-upstream"/, "upstream block exists");
  assert.match(html, /代码实现/, "upstream node name is shown");
  assert.match(html, /已实现登录页并补了回归测试/, "upstream output summary is shown");
});

test("没有上游时不留空块", () => {
  const bare = { ...gate, payload_json: JSON.stringify({ prompt: "确认", node_id: "verify" }) };
  const html = renderToString(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(TaskHumanGateCard, { activity: bare, onDecision: () => {} }),
    ),
  );
  assert.doesNotMatch(html, /data-testid="task-human-gate-upstream"/);
});

test("审批卡提供附言输入框，普通输入框仍然禁用", () => {
  const html = renderCard({});
  assert.match(html, /data-testid="task-human-gate-note"/, "gate card owns the note field");
  // 普通留言框在此状态下整体换成提示（D4）：连接层的 ConversationView 必须
  // 在 pendingTaskPrompt 时渲染 conversation-prompt-pending，而不是 ActivityInput。
  const view = read("../src/components/task/ConversationView.tsx");
  assert.match(view, /pendingTaskPrompt\(activities\)/, "the view derives the pending prompt");
  assert.match(view, /data-testid="conversation-prompt-pending"/, "composer is replaced, not enabled");
  assert.match(view, /t\("task\.activity\.gatePending"\)/, "gate pending copy names the approval");
});

test("批准时带上附言，空附言不发", () => {
  const card = read("../src/components/task/TaskHumanGateCard.tsx");
  assert.match(card, /note/, "card keeps a note state");
  assert.match(card, /onDecision\?\.\(activity, "approve", /, "approve carries the note");
  const svc = read("../src/services/task/humanGate.ts");
  assert.match(svc, /note\?: string/, "service input accepts a note");
  assert.match(svc, /\.\.\.\(trimmedNote \? \{ note: trimmedNote \} : \{\}\)/, "empty note is omitted from the body");
});

test("已自动通过的旧末端 gate 有明确标记", () => {
  const auto = {
    ...gate,
    status: "completed",
    payload_json: JSON.stringify({ prompt: "验收", node_id: "verify", auto_approved: true }),
  };
  const html = renderToString(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(TaskHumanGateCard, { activity: auto }),
    ),
  );
  assert.match(html, /data-testid="task-human-gate-auto"/);
  assert.doesNotMatch(html, /task-human-gate-approve/, "nothing left to approve");
});
