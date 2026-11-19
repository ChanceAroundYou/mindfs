import assert from "node:assert/strict";
import { test } from "vitest";
import React from "react";
import { renderToString } from "react-dom/server";
import { I18nProvider } from "../src/i18n/index.tsx";
import { SessionCardMemo } from "../src/components/session/SessionCard.tsx";
import type { SessionItem } from "../src/components/session/sessionListTypes.ts";

// 侧栏名字是单行截断的：任务名 · 节点名 · 触发留言 这种长名字在 260px 下
// 后半段会被省略号吃掉，而区分同一任务不同轮次的正是后半段。悬停 title
// 是补这条信息的唯一入口，丢了用户就只剩一排看起来一样的名字。

function session(name: string): SessionItem {
  return {
    key: "k1",
    root_id: "root",
    name,
    updated_at: "2026-11-19T20:12:00Z",
  } as unknown as SessionItem;
}

function render(name: string): string {
  return renderToString(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(SessionCardMemo, {
        session: session(name),
        sessionByKey: new Map(),
        selected: false,
      }),
    ),
  );
}

test("session-card-name-tooltip", () => {
  const longName = "任务详情面板修改 / #87 · 实现 · 把运行视图改成多轮展示并补一个会话图标";
  const html = render(longName);

  assert.match(
    html,
    new RegExp(`title="${longName}"`),
    "session name span must expose the full name on hover",
  );
  // 布局不动：名字仍是一行、超出打省略号。title 是补信息，不是换行方案。
  const span = html.match(/<span title="[^"]*"/);
  assert.ok(span, "title must sit on a span");
  assert.match(
    html,
    /white-space:\s*nowrap/,
    "name must stay single-line (no row-height change)",
  );
  assert.match(html, /text-overflow:\s*ellipsis/, "long names must still be ellipsized");
});
