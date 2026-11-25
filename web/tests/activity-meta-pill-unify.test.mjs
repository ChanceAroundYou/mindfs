import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import React from "react";
import { renderToString } from "react-dom/server";
import { I18nProvider } from "../src/i18n/index.tsx";
import { ActivityMessage } from "../src/components/task/ActivityMessage.tsx";

// 用户 2026-11-25 截图报「提醒字段样式不统一」：同一档元信息里，Agent 页脚的
// 「结果」比系统行的「提醒」浅一半。原因是页脚容器挂了 opacity: 0.5，而胶囊的
// `opacity: 1` 覆盖不了父级 —— 父级不透明度是整棵子树合成的，子级写什么都没用。
// 这里钉住两件事：三种消息的胶囊是同一份外形、行容器不许再挂 opacity。

const base = {
  id: "a1",
  task_id: "task-1",
  type: "agent_reply",
  author: "agent",
  role: "agent",
  body: "正文",
  workflow_choice: "empty",
  created_at: "2026-11-12T08:00:00Z",
};

const render = (node) => renderToString(React.createElement(I18nProvider, null, node));

function pillStyles(html) {
  return [...html.matchAll(/data-testid="activity-type-tag"[^>]*style="([^"]*)"/g)].map((m) => m[1]);
}

test("三种消息的类型胶囊是同一份外观", () => {
  const html = render(
    React.createElement("div", null,
      React.createElement(ActivityMessage, { activity: { ...base, type: "user_message", role: "user" } }),
      React.createElement(ActivityMessage, { activity: base }),
      React.createElement(ActivityMessage, { activity: { ...base, type: "attention", role: "system" } }),
    ),
  );
  const styles = pillStyles(html);
  assert.equal(styles.length, 3, "用户 / Agent / 系统三条消息都要有类型胶囊");
  for (const style of styles.slice(1)) {
    assert.equal(style, styles[0], "胶囊外形只有一份定义，不能按分支各写一套");
  }
  assert.match(styles[0], /font-size:10px/, "和页脚时间同一档字号");
  assert.match(styles[0], /border-radius:999px/);
});

test("排队徽标与类型胶囊共用同一份胶囊外形", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: { ...base, type: "user_message", role: "user", status: "queued" },
      onRunQueued: () => {},
    }),
  );
  const tag = pillStyles(html)[0];
  const queued = [...html.matchAll(/<span style="([^"]*)">[^<]*<\/span>/g)]
    .map((m) => m[1])
    .find((s) => s.includes("border-radius:999px") && s === tag);
  assert.equal(queued, tag, "「待执行」徽标就是同一枚胶囊，不许另抄一套字号/内边距");
  assert.match(html, /data-testid="activity-run-queued"[^>]*style="[^"]*line-height:1\.5/);
});

test("元信息行容器不挂 opacity：胶囊恒为满不透明", () => {
  const html = render(
    React.createElement("div", null,
      React.createElement(ActivityMessage, { activity: base }),
      React.createElement(ActivityMessage, { activity: { ...base, type: "user_message", role: "user" } }),
      // 提醒走 Agent 页脚那一档（2026-11-25 最终形态），系统行的样式由 system_event 覆盖。
      React.createElement(ActivityMessage, { activity: { ...base, type: "attention", role: "system" } }),
      React.createElement(ActivityMessage, { activity: { ...base, type: "system_event", role: "system" } }),
    ),
  );
  // 两个 Agent 骨架（回复、提醒）的页脚都不许挂 opacity。
  const footers = [...html.matchAll(/data-testid="activity-footer"[^>]*style="([^"]*)"/g)].map((m) => m[1]);
  assert.equal(footers.length, 2, "agent 与 attention 各一个页脚");
  for (const footer of footers) assert.doesNotMatch(footer, /opacity/, "行容器的不透明度会连同胶囊一起压淡");
  // 用户 / 系统的元信息行是匿名 div：取所有含 time 样式的那一层，同样不许挂 opacity。
  const rows = [...html.matchAll(/<div style="([^"]*)"><span data-testid="activity-type-tag"/g)].map((m) => m[1]);
  assert.equal(rows.length, 2, "user/system meta rows must render the chip");
  for (const row of rows) assert.doesNotMatch(row, /opacity/);
});

test("三条分支的时间文案同一档：10px + 次要色 + opacity 0.5", () => {
  const html = render(
    React.createElement("div", null,
      React.createElement(ActivityMessage, { activity: base }),
      React.createElement(ActivityMessage, { activity: { ...base, type: "user_message", role: "user" } }),
      React.createElement(ActivityMessage, { activity: { ...base, type: "attention", role: "system" } }),
    ),
  );
  const times = [...html.matchAll(/<time[^>]*style="([^"]*)"/g)].map((m) => m[1]);
  assert.equal(times.length, 3);
  // Agent 页脚的时间继承行上的 10px；用户/系统的时间自带字号。三者都必须半透明。
  for (const style of times) {
    assert.match(style, /opacity:0\.5/, "时间文案统一用 0.5，系统行此前是 0.7");
    assert.doesNotMatch(style, /font-size:(?!10px)/);
  }
  const footer = html.match(/data-testid="activity-footer"[^>]*style="([^"]*)"/);
  assert.match(footer[1], /font-size:10px/);
});

test("胶囊样式只有一份定义，组件里不许再抄 999 圆角", () => {
  const tag = readFileSync(new URL("../src/components/task/ActivityTypeTag.tsx", import.meta.url), "utf8");
  const message = readFileSync(new URL("../src/components/task/ActivityMessage.tsx", import.meta.url), "utf8");
  assert.match(tag, /export const activityMetaPillStyle: React\.CSSProperties = \{/);
  const pills = message.match(/borderRadius: 999,/g) || [];
  // 只剩「立即执行」这一个带强调色的按钮自己定义胶囊外形。
  assert.equal(pills.length, 1, "类型胶囊与排队徽标必须复用 activityMetaPillStyle");
  assert.match(message, /import \{ ActivityTypeTag, activityMetaPillStyle \} from "\.\/ActivityTypeTag";/);
});
