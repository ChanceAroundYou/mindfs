// 任务对话里的提问卡与 HumanGate 审批卡（WP0）。
//
// 单独成文件：conversation-view.test.mjs 负责留言流/气泡/输入框，本文件负责
// question / human_gate 两种专用卡与「待处理时挡输入」的判定。两者都超过
// 500 行就违反 file-size 硬线。

import assert from "node:assert/strict";
import path from "node:path";
import fs from "./source-map-fs.mjs";
import React from "react";
import { renderToString } from "react-dom/server";
import { test } from "vitest";
import { I18nProvider } from "../src/i18n/index.tsx";
import { ActivityMessage } from "../src/components/task/ActivityMessage.tsx";
import { pendingTaskPrompt } from "../src/shared/activityModel.ts";

const render = (node) => renderToString(React.createElement(I18nProvider, null, node));

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

// ask_user 事件落到任务对话后必须是一张可回答的卡，而不是普通 agent 气泡。
test("ActivityMessage renders a waiting question as an answerable card", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: {
        ...baseActivity,
        type: "question",
        role: "agent",
        status: "waiting_user",
        body: "需要确认：继续执行吗？",
        session_key: "s-1",
        tool_use_id: "tool-1",
        payload_json: JSON.stringify({
          tool_use_id: "tool-1",
          agent: "claude",
          questions: [{ question: "继续执行吗？", options: [{ label: "继续" }, { label: "停止" }] }],
        }),
      },
      rootId: "root-1",
      onAnswerQuestion: () => {},
    }),
  );
  assert.match(html, /data-testid="task-question-card"/);
  assert.match(html, /继续执行吗？/);
  assert.match(html, /继续/);
  assert.match(html, /停止/);
});

// HumanGate 是专用控件：只有批准/驳回/停止，普通留言输入不允许代替决定。
test("ActivityMessage renders a waiting human gate with dedicated actions", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: {
        ...baseActivity,
        type: "human_gate",
        role: "system",
        status: "waiting_user",
        body: "等待你确认：确认可以上线",
        payload_json: JSON.stringify({ prompt: "确认可以上线", node_id: "gate", node_run_id: "nr-1" }),
      },
      onGateDecision: () => {},
    }),
  );
  assert.match(html, /data-testid="task-human-gate"/);
  assert.match(html, /确认可以上线/);
  assert.match(html, /data-testid="task-human-gate-approve"/);
  assert.match(html, /data-testid="task-human-gate-reject"/);
  assert.match(html, /data-testid="task-human-gate-stop"/);
});

// 暂停时不给审批按钮，只给恢复：停止不是替用户做决定。
test("a paused human gate offers resume instead of approval", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: {
        ...baseActivity,
        type: "human_gate",
        role: "system",
        status: "waiting_user",
        payload_json: JSON.stringify({ prompt: "确认可以上线", node_id: "gate" }),
      },
      paused: true,
      onGateDecision: () => {},
      onResumeTask: () => {},
    }),
  );
  assert.match(html, /data-testid="task-human-gate-resume"/);
  assert.doesNotMatch(html, /data-testid="task-human-gate-approve"/);
});

test("pendingTaskPrompt picks the oldest unanswered prompt and ignores answered ones", () => {
  const answered = {
    ...baseActivity,
    id: "q-old",
    type: "question",
    status: "completed",
    payload_json: JSON.stringify({ questions: [{ question: "旧" }], answers: { q_0: "好" } }),
  };
  const waiting = {
    ...baseActivity,
    id: "q-new",
    type: "question",
    status: "waiting_user",
    payload_json: JSON.stringify({ questions: [{ question: "新" }] }),
  };
  const gate = {
    ...baseActivity,
    id: "g-1",
    type: "human_gate",
    status: "waiting_user",
    payload_json: JSON.stringify({ prompt: "确认" }),
  };
  assert.equal(pendingTaskPrompt([answered]), null);
  assert.equal(pendingTaskPrompt([answered, waiting, gate])?.id, "q-new");
  assert.equal(pendingTaskPrompt([answered, gate])?.id, "g-1");
  assert.equal(pendingTaskPrompt([]), null);
});

test("pendingTaskPrompt tolerates malformed payload JSON", () => {
  const broken = {
    ...baseActivity,
    id: "q-broken",
    type: "question",
    status: "waiting_user",
    payload_json: "{not json",
  };
  assert.equal(pendingTaskPrompt([broken])?.id, "q-broken");
});

test("waiting gate cards surface submission errors", () => {
  const gate = render(
    React.createElement(ActivityMessage, {
      activity: {
        ...baseActivity,
        type: "human_gate",
        role: "system",
        status: "waiting_user",
        payload_json: JSON.stringify({ prompt: "确认", node_id: "gate" }),
      },
      promptError: "decision failed",
      onGateDecision: () => {},
    }),
  );
  assert.match(gate, /decision failed/);
});

test("the shared ask card shows submit failures instead of swallowing them", () => {
  const source = fs.readFileSync(
    path.resolve(import.meta.dirname, "../src/components/session/AskUserQuestionCard.tsx"),
    "utf8",
  );
  assert.match(source, /data-testid="ask-user-submit-error"/);
  assert.match(source, /setSubmitError\(err instanceof Error \? err\.message : String\(err\)\)/);
});

test("a decided gate does not keep showing a stale error", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: {
        ...baseActivity,
        type: "human_gate",
        role: "system",
        status: "completed",
        payload_json: JSON.stringify({ prompt: "确认", node_id: "gate", decision: "approve" }),
      },
      promptError: "stale",
    }),
  );
  assert.doesNotMatch(html, /stale/);
});

test("useTaskActivities owns the answer and gate writes", () => {
  const hook = fs.readFileSync(
    path.resolve(import.meta.dirname, "../src/hooks/useTaskActivities.ts"),
    "utf8",
  );
  // 回答必须走会话 WS（与 SessionViewer 同一条链路），审批必须走专用端点；
  // 组件层不得直接 import services（reviewer-architecture-rules）。
  assert.match(hook, /sessionService\.answerQuestion\(/);
  assert.match(hook, /postTaskHumanGate\(/);
  assert.match(hook, /applyActivityUpdated\(prev, activity\.id, \{/);
});