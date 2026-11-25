import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import React from "react";
import { renderToString } from "react-dom/server";
import { I18nProvider } from "../src/i18n/index.tsx";
import { ActivityMessage } from "../src/components/task/ActivityMessage.tsx";

// 2026-11-25：提醒先试过居中气泡，用户最终定了「居左正文，和普通 assistant 消息
// 基本一致」—— 气泡、灰底、内边距全部去掉，只留正文颜色一档差异。
// 三种身份的排布：Agent 正文无气泡 / 用户右气泡 / 提醒居左正文（与 Agent 同骨架）。

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

test("提醒是居左正文：与 Agent 回复同一骨架，没有气泡/灰底/内边距", () => {
  assert.doesNotMatch(attention, /data-testid="activity-attention-bubble"/);
  // 行容器复用 agentMessageStyle：左对齐的纵向 flex，不是居中容器。
  const row = styleOf(attention, "activity-system");
  assert.match(row, /flex-direction:column/);
  assert.doesNotMatch(row, /align-items:center/);
  const body = styleOf(attention, "activity-attention-body");
  assert.doesNotMatch(body, /background/);
  assert.doesNotMatch(body, /padding/);
  assert.doesNotMatch(body, /border-radius/);
  assert.doesNotMatch(body, /text-align:center/);
});

test("提醒按 severity 着色：info 浅灰 / warning 黄 / error 红，全部走现有 token", () => {
  const info = render({ ...base, type: "attention", body: "x", severity: "info" });
  const warning = render({ ...base, type: "attention", body: "x", severity: "warning" });
  const error = render({ ...base, type: "attention", body: "x", severity: "error" });
  // 旧数据没有 severity 列，必须落回 info，而不是被画成告警色。
  const legacy = render({ ...base, type: "attention", body: "x" });

  assert.match(styleOf(info, "activity-attention-body"), /color:var\(--text-secondary\)/);
  assert.match(styleOf(legacy, "activity-attention-body"), /color:var\(--text-secondary\)/);
  assert.match(styleOf(warning, "activity-attention-body"), /color:var\(--status-warn\)/);
  assert.match(styleOf(error, "activity-attention-body"), /color:var\(--status-bad\)/);
  assert.match(legacy, /data-severity="info"/);

  // 胶囊只换文字颜色，外形仍是同一份 activityMetaPillStyle。
  assert.match(styleOf(warning, "activity-type-tag"), /color:var\(--status-warn\)/);
  assert.match(styleOf(error, "activity-type-tag"), /border-radius:999px[^"]*color:var\(--status-bad\)/);
  assert.match(styleOf(info, "activity-type-tag"), /border-radius:999px[^"]*color:var\(--text-secondary\)/);

  // 不写死色值：深浅主题各自给值。
  for (const html of [info, warning, error, legacy]) {
    assert.doesNotMatch(styleOf(html, "activity-attention-body"), /#[0-9a-f]{3,6}/i);
  }
  const source = readFileSync(new URL("../src/components/task/ActivityMessage.tsx", import.meta.url), "utf8");
  assert.match(source, /export function attentionSeverityColor\(severity: string \| undefined\): string \{/);
  assert.match(source, /return "var\(--status-warn\)";/);
  assert.match(source, /return "var\(--status-bad\)";/);
});

test("提醒的元信息与 Agent 页脚同一档", () => {
  const chip = attention.indexOf('data-testid="activity-type-tag"');
  const bubbleAt = attention.indexOf('data-testid="activity-attention-body"');
  const footerAt = attention.indexOf('data-testid="activity-footer"');
  assert.ok(bubbleAt >= 0 && footerAt > bubbleAt, "正文在前、页脚在后");
  assert.ok(chip > footerAt, "类型胶囊收在页脚里，和 Agent 回复一致");
  // 页脚容器不挂 opacity，胶囊恒为满不透明（见 activity-meta-pill-unify）。
  assert.doesNotMatch(styleOf(attention, "activity-footer"), /opacity/);
});

test("普通系统事件仍是居中一行，用户气泡与 Agent 正文不受影响", () => {
  assert.doesNotMatch(systemEvent, /data-testid="activity-attention-body"/);
  assert.match(styleOf(systemEvent, "activity-system"), /align-items:center/);
  assert.match(styleOf(user, "activity-user"), /align-items:flex-end/);
  assert.doesNotMatch(agent, /data-testid="activity-attention-body"/);
  assert.match(styleOf(agent, "activity-agent"), /align-items:stretch|flex-direction:column/);
});
