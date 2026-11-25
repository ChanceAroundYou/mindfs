import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import React from "react";
import { renderToString } from "react-dom/server";
import { I18nProvider } from "../src/i18n/index.tsx";
import { ActivityMessage } from "../src/components/task/ActivityMessage.tsx";

// 2026-11-25：提醒原来和普通系统事件共用一条居中灰字，夹在左右气泡中间很突兀。
// 现在三种身份的排布是：Agent 正文无气泡 / 用户右气泡 / 提醒居中气泡且文本左对齐。

const base = {
  id: "a1",
  task_id: "task-1",
  author: "system",
  role: "system",
  workflow_choice: "empty",
  created_at: "2026-11-25T12:22:00Z",
};

const render = (activity) => renderToString(
  React.createElement(I18nProvider, null, React.createElement(ActivityMessage, { activity })),
);

const styleOf = (html, testId) =>
  html.match(new RegExp(`data-testid="${testId}"[^>]*style="([^"]*)"`))?.[1] ?? "";

const attention = render({ ...base, type: "attention", body: "服务已重启，请继续" });
const systemEvent = render({ ...base, type: "system_event", body: "工作流已切换" });
const agent = render({ ...base, type: "agent_reply", role: "agent", author: "agent", body: "做完了" });
const user = render({ ...base, type: "user_message", role: "user", author: "user", body: "继续" });

test("提醒渲染成居中气泡：四角一致、灰底、文本左对齐", () => {
  assert.match(attention, /data-testid="activity-attention-bubble"/);
  const bubble = styleOf(attention, "activity-attention-bubble");
  assert.match(bubble, /border-radius:12px/, "不是用户气泡那种带方向的尾巴");
  assert.match(bubble, /text-align:left/);
  assert.match(bubble, /background:rgba\(148,\s*163,\s*184,\s*0\.14\)/);
  assert.match(bubble, /font-size:14px/, "和用户消息同一档正文");
  assert.match(bubble, /max-width:min\(80%,\s*640px\)/, "长提醒也要能换行");
  // 外层仍然居中，气泡本身不会贴左或贴右。
  assert.match(styleOf(attention, "activity-system"), /align-items:center/);
});

test("提醒气泡复用用户气泡的外形，不新造一套配色", () => {
  const userBubble = styleOf(user, "activity-user");
  const bubble = styleOf(attention, "activity-attention-bubble");
  // 取用户气泡的实际定义（用户消息的内层没有 testid，按灰底样式定位）。
  const userPill = user.match(/<div style="([^"]*background:rgba\(148,\s*163,\s*184,\s*0\.14\)[^"]*)"/)?.[1] ?? "";
  assert.ok(userPill, "用户气泡必须渲染");
  for (const prop of ["padding:10px 16px", "color:var(--text-primary)", "line-height:1.5", "font-size:14px"]) {
    assert.match(bubble, new RegExp(prop.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));
    assert.match(userPill, new RegExp(prop.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));
  }
  assert.match(userPill, /border-radius:18px 18px 4px 18px/, "用户气泡仍靠右尾巴表达身份");
  assert.match(styleOf(user, "activity-user"), /align-items:flex-end/);
  assert.ok(!userBubble.includes("background"), "用户行容器本身不该是气泡");
});

test("Agent 正文仍无气泡，系统事件仍是一行中性文本", () => {
  assert.doesNotMatch(agent, /data-testid="activity-attention-bubble"/);
  assert.doesNotMatch(agent, /background:rgba\(148,\s*163,\s*184,\s*0\.14\)/);
  assert.doesNotMatch(systemEvent, /data-testid="activity-attention-bubble"/);
  assert.match(systemEvent, /text-align:center/);
  assert.doesNotMatch(systemEvent, /background:rgba\(148,\s*163,\s*184,\s*0\.14\)/);
});

test("只改变正文外壳：提醒的类型胶囊与时间仍在气泡下方", () => {
  const tagAt = attention.indexOf('data-testid="activity-type-tag"');
  const bubbleAt = attention.indexOf('data-testid="activity-attention-bubble"');
  const timeAt = attention.indexOf("<time");
  assert.ok(bubbleAt >= 0 && tagAt > bubbleAt && timeAt > tagAt);
  const source = readFileSync(new URL("../src/components/task/ActivityMessage.tsx", import.meta.url), "utf8");
  assert.match(source, /const attentionBubbleStyle: React\.CSSProperties = \{\n  \.\.\.userBubbleStyle,/);
});
