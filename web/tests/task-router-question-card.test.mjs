// Router 发起的任务提问卡（G-QG）。
//
// Router 的 ask_user 现在落成真正的 type=question 卡：选项是按钮、输入框被挡、
// 作答成为一条用户留言。这里钉住前端这一半 —— 卡片在没有会话/tool call 时
// 也能提交（提交按钮的可用性靠选择后才成立，服务端渲染只能钉结构与守卫），
// 而「有会话却缺 tool id」的坏数据仍按原来的阻挡；作答分流到任务留言端点，
// 不会被塞进一个不存在的会话。

import assert from "node:assert/strict";
import fs from "node:fs";
import React from "react";
import { renderToString } from "react-dom/server";
import { test } from "vitest";
import { I18nProvider } from "../src/i18n/index.tsx";
import { ActivityMessage } from "../src/components/task/ActivityMessage.tsx";

const render = (node) => renderToString(React.createElement(I18nProvider, null, node));

const routerQuestionActivity = {
  id: "act-router-question",
  task_id: "task-1",
  type: "question",
  role: "agent",
  status: "waiting_user",
  body: "先做哪个？\n· A\n· B",
  workflow_choice: "empty",
  created_at: "2026-11-23T08:00:00Z",
  // 关键：Router 不在任何会话里，这两种 id 都是空的。
  session_key: "",
  tool_use_id: "",
  payload_json: JSON.stringify({
    source: "router",
    questions: [
      { question: "先做哪个？", header: "需要你拍板", options: [{ label: "A" }, { label: "B" }] },
    ],
  }),
};

test("Router 提问卡渲染成可点的选项卡（不是一条没说完的话）", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: routerQuestionActivity,
      rootId: "root-1",
      onAnswerQuestion: () => {},
    }),
  );
  assert.match(html, /data-testid="task-question-card"/);
  assert.match(html, /先做哪个？/);
  // 两个选项是 radio 行，而不是正文里的两行字。
  assert.equal((html.match(/type="radio"/g) || []).length, 2);
  assert.match(html, /正文<\/span>|>A<\/span>/);
  // 还没选答案：提交键必须是禁用态，不能一点就派一个空答案出去。
  assert.match(html, /<button type="button" disabled=""/);
});

const read = (rel) => fs.readFileSync(new URL(`../${rel}`, import.meta.url), "utf8");

test("提交守卫：Router 卡（无会话/无 tool id）放行，坏掉的会话卡仍拦截", () => {
  const card = read("src/components/session/AskUserQuestionCard.tsx");
  assert.match(card, /const sessionBound = !!sessionKey;/);
  assert.match(card, /const routerCard = !sessionBound && !toolUseId;/);
  assert.match(card, /\(sessionBound \? !!toolUseId : routerCard\)/);
  // payload 里两个 id 都用空串兜底：Router 卡没有目标，不能传 undefined 让调用方猜。
  assert.match(card, /sessionKey: sessionKey \|\| "",/);
  assert.match(card, /toolUseId: toolUseId \|\| "",/);
});

test("作答分流：Router 卡走任务留言端点，会话卡仍走会话回答", () => {
  const hook = read("src/hooks/useTaskActivities.ts");
  assert.match(hook, /answerTaskQuestion/);
  assert.match(hook, /const routerCard = payload\.source === "router" \|\| \(!sessionKey && !toolUseId\)/);
  assert.match(hook, /if \(routerCard\) \{\s*await answerTaskQuestion\(/);
  // 会话分支必须留在原地：两种卡的答案去处不同，不能只留一条路。
  assert.match(hook, /sessionService\.answerQuestion\(/);

  const service = read("src/services/activities.ts");
  assert.match(service, /activities\/\$\{encodeURIComponent\(activityId\)\}\/answer/);
  assert.match(service, /root_id: requiredRoot\(rootId\), answers/);
});