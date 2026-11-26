import assert from "node:assert/strict";
import { test } from "vitest";
import React from "react";
import { renderToString } from "react-dom/server";
import { I18nProvider, translateNow } from "../src/i18n/index.tsx";
import { TaskCardRows } from "../src/components/task/TaskCardRows.tsx";
import { taskRouterAskBadge } from "../src/components/task/routerAskModel.ts";

// 任务卡上的两类 Router 来源标记：
//   1. router_ask —— Router 自己派发的提问（session_key 为空的 waiting_user 卡）；
//      会话 worker 透传的 ask 仍走 aux_flags.ask_user_waiting，两者必须分得开。
//   2. latest_attention —— 最新一条提醒，按 severity（info/warning/error）着色。

const baseTask = {
  id: "task-1",
  root_id: "root-1",
  status: "waiting_user",
  created_at: "2026-11-25T10:00:00Z",
  updated_at: "2026-11-25T10:00:00Z",
};

const cardHtml = (task) =>
  renderToString(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(TaskCardRows, {
        task,
        sessionKeys: [],
        sessionByKey: {},
        getNodeColor: () => null,
        onOpenSession: () => {},
        onMove: () => {},
        onShowSessionError: () => {},
      }),
    ),
  );

test("router_ask 纯函数：缺失不产生角标，count 归一到 >=1，问题正文去空白", () => {
  assert.equal(taskRouterAskBadge(null), null);
  assert.equal(taskRouterAskBadge(undefined), null);
  assert.equal(taskRouterAskBadge({}), null);
  assert.equal(taskRouterAskBadge({ router_ask: undefined }), null);

  const badge = taskRouterAskBadge({
    router_ask: { count: 0, since: "2026-11-25T09:00:00Z", question: "  走哪个？ " },
  });
  assert.equal(badge.key, "router_ask");
  assert.equal(badge.count, 1);
  assert.equal(badge.question, "走哪个？");
  assert.equal(badge.since, "2026-11-25T09:00:00Z");
  assert.equal(badge.attention, true);
});

test("Router ask 与 worker ask 的徽标 key/文案/图标互不混用", () => {
  const workerLabel = translateNow("task.waitingUser");
  const routerLabel = translateNow("task.routerAskWaitingTitle", { question: "走哪个？" });

  // 只有 worker ask：不能出现 Router 的文案或圆圈图标。
  const workerOnly = cardHtml({
    ...baseTask,
    aux_flags: { ask_user_waiting: true },
  });
  assert.ok(workerOnly.includes(workerLabel), "worker ask 用 task.waitingUser");
  assert.ok(!workerOnly.includes(routerLabel), "worker ask 不得冒充 Router ask");
  assert.ok(!workerOnly.includes('stroke="#ef4444"'), "worker ask 不得画成 Router 图标");

  // 只有 Router ask：不能出现 worker 的问号文案；图标统一用告警红。
  const routerOnly = cardHtml({
    ...baseTask,
    router_ask: { count: 1, question: "走哪个？" },
  });
  assert.ok(routerOnly.includes(routerLabel), "Router ask 带上问题正文");
  assert.ok(routerOnly.includes('stroke="#ef4444"'), "Router ask 图标与会话 ask 同为红色");
  assert.ok(!routerOnly.includes(workerLabel), "Router ask 不得复用 worker 文案");

  // 两者同时存在：各画各的，不合并、不互相吞掉。
  const both = cardHtml({
    ...baseTask,
    aux_flags: { ask_user_waiting: true },
    router_ask: { count: 2, question: "走哪个？" },
  });
  assert.ok(both.includes(workerLabel));
  assert.ok(both.includes(routerLabel));
  assert.ok(both.includes('stroke="#ef4444"'));
});

test("最新提醒沿用类型胶囊 + 正文，按 severity 着色并可回退 info", () => {
  const pill = translateNow("task.activity.type.attention");
  const warning = cardHtml({
    ...baseTask,
    latest_attention: { body: "复核不可用，先把结果交回给你", severity: "warning", created_at: "2026-11-25T11:00:00Z" },
  });
  assert.ok(warning.includes("复核不可用，先把结果交回给你"), "正文上卡");
  assert.ok(warning.includes(pill), "复用提醒类型胶囊");
  assert.ok(warning.includes('data-severity="warning"'));
  assert.ok(warning.includes("var(--status-warn)"), "warning 用全局告警色");

  const error = cardHtml({
    ...baseTask,
    latest_attention: { body: "投递失败", severity: "error" },
  });
  assert.ok(error.includes('data-severity="error"'));
  assert.ok(error.includes("var(--status-bad)"), "error 用全局错误色");

  const legacy = cardHtml({
    ...baseTask,
    latest_attention: { body: "存量提醒" },
  });
  assert.ok(legacy.includes('data-severity="info"'), "空 severity 归一到 info");
  assert.ok(legacy.includes("var(--text-secondary)"), "info 沿用次要文字色");

  const none = cardHtml(baseTask);
  assert.ok(!none.includes('data-testid="task-card-attention"'), "没有提醒就不画");
});
