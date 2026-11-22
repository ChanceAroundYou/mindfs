import assert from "node:assert/strict";
import { test } from "vitest";
import React from "react";
import { renderToString } from "react-dom/server";
import { I18nProvider, translateNow } from "../src/i18n/index.tsx";
import { TaskCardRows } from "../src/components/task/TaskCardRows.tsx";
import { taskRouterAlertBadge } from "../src/components/task/routerAlertModel.ts";

// 任务卡上的 Router 角标（W3-4）：重启打断了回合（turn_interrupted）或一轮长时间
// 没进展（turn_stalled）且 Router 还没处理时点亮，处理完服务端就不再下发
// router_alert，卡片自动恢复干净。
//
// 「未处理」的判据在服务端（事件账本里还是 pending/claimed），这里钉两件事：
// 纯函数怎么把派生字段翻成角标，以及卡片真的把它画出来 / 真的会消失。

const baseTask = {
  id: "task-alert",
  root_id: "root-1",
  task_template_id: "",
  task_template_name: "",
  status: "waiting_user",
  created_at: "2026-11-22T10:00:00Z",
  updated_at: "2026-11-22T10:00:00Z",
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

test("没有 router_alert 就没有角标", () => {
  assert.equal(taskRouterAlertBadge(null), null);
  assert.equal(taskRouterAlertBadge(undefined), null);
  assert.equal(taskRouterAlertBadge({}), null);
  assert.equal(taskRouterAlertBadge({ router_alert: undefined }), null);
});

test("中断与停滞各翻成一个角标，文案键与告警级别正确", () => {
  const interrupted = taskRouterAlertBadge({
    router_alert: { kind: "turn_interrupted", count: 2, since: "2026-11-22T09:58:00Z" },
  });
  assert.deepEqual(interrupted, {
    key: "router_interrupted",
    labelKey: "task.routerAlertInterrupted",
    count: 2,
    attention: true,
    since: "2026-11-22T09:58:00Z",
  });

  const stalled = taskRouterAlertBadge({
    router_alert: { kind: "turn_stalled", count: 1, since: "2026-11-22T09:50:00Z" },
  });
  assert.equal(stalled.key, "router_stalled");
  assert.equal(stalled.labelKey, "task.routerAlertStalled");
  assert.equal(stalled.attention, true);
});

test("未知 kind 不画角标，count 缺失或非法一律当 1", () => {
  assert.equal(taskRouterAlertBadge({ router_alert: { kind: "run_failed" } }), null);

  for (const count of [undefined, 0, -3, Number.NaN, "abc"]) {
    const badge = taskRouterAlertBadge({ router_alert: { kind: "turn_stalled", count } });
    assert.equal(badge.count, 1, `count=${String(count)} 应夹到 1`);
  }
  const missingSince = taskRouterAlertBadge({ router_alert: { kind: "turn_stalled" } });
  assert.equal(missingSince.since, "");
});

test("卡片画出角标，事件处理完（字段消失）后角标不再出现", () => {
  const interruptedLabel = translateNow("task.routerAlertInterrupted", { count: "1" });
  const stalledLabel = translateNow("task.routerAlertStalled", { count: "2" });

  const withInterrupt = cardHtml({
    ...baseTask,
    router_alert: { kind: "turn_interrupted", count: 1, since: "2026-11-22T09:58:00Z" },
  });
  assert.ok(withInterrupt.includes(interruptedLabel), "中断角标带上 i18n 文案");
  assert.ok(withInterrupt.includes("#ef4444"), "中断角标用红色图标");

  const withStalled = cardHtml({
    ...baseTask,
    router_alert: { kind: "turn_stalled", count: 2, since: "2026-11-22T09:50:00Z" },
  });
  assert.ok(withStalled.includes(stalledLabel), "停滞角标带上条数");
  assert.ok(withStalled.includes("#f59e0b"), "停滞角标用琥珀色图标");

  const clean = cardHtml(baseTask);
  assert.ok(!clean.includes(interruptedLabel), "没有未处理事件时中断角标不出现");
  assert.ok(!clean.includes(stalledLabel), "没有未处理事件时停滞角标不出现");
});
