import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import React from "react";
import { renderToString } from "react-dom/server";
import { I18nProvider, translateNow } from "../src/i18n/index.tsx";
import { SupervisionEntryList } from "../src/components/task/SupervisionEntryList.tsx";
import {
  isSupervisionDecision,
  supervisionActionKey,
  supervisionRowClock,
  supervisionRows,
  supervisionStatusKey,
  supervisionTriggerKey,
} from "../src/components/task/supervisionEntryModel.ts";

// 监管记录入口（S4）：任务详情里把 Router 自己动过手的回合列出来。
//
// 数据来源是既有端点 GET /api/tasks/{id}/router-decisions（时间倒序）。这里只
// 关心三件事：哪些决策算「监管」、按什么顺序展示、每行怎么读。视图本身拿
// 纯函数的输出渲染，不做二次筛选。

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

const decision = (id, trigger, extra = {}) => ({
  id,
  task_id: "task-1",
  trigger_kind: trigger,
  status: "applied",
  created_at: "2026-11-20T10:00:00Z",
  ...extra,
});

test("只列监管决策：监管触发或带监管动作，普通留言回合不占行", () => {
  assert.equal(isSupervisionDecision(decision("d1", "user_message")), false);
  assert.equal(isSupervisionDecision(decision("d2", "run_settled")), false);
  assert.equal(isSupervisionDecision(decision("d3", "run_failed")), true);
  assert.equal(isSupervisionDecision(decision("d4", "run_stalled")), true);
  assert.equal(isSupervisionDecision(decision("d5", "direct_stalled")), true);
  assert.equal(
    isSupervisionDecision(decision("d6", "user_message", { action_kinds: ["say", "retry_node"] })),
    true,
    "a user turn that ended in a supervision action still belongs here",
  );
  assert.deepEqual(
    supervisionRows([
      decision("d1", "user_message", { action_kinds: ["say"] }),
      decision("d2", "direct_stalled", { action_kinds: ["retry_direct"] }),
    ]).map((row) => row.id),
    ["d2"],
  );
});

test("每行只保留监管动作，去重且保持后端给的顺序", () => {
  const [row] = supervisionRows([
    decision("d1", "run_failed", { action_kinds: ["retry_node", "notify_user", "retry_node", "say"] }),
  ]);
  assert.deepEqual(row.actions, ["retry_node", "notify_user"]);
  assert.deepEqual(row.trigger, "run_failed");
  assert.equal(row.status, "applied");
});

test("按时间倒序；同一时刻保持后端顺序（后端已经是倒序）", () => {
  const rows = supervisionRows([
    decision("old", "run_stalled", { created_at: "2026-11-20T09:00:00Z" }),
    decision("new", "run_failed", { created_at: "2026-11-20T11:00:00Z" }),
    decision("tie-a", "direct_stalled", { created_at: "2026-11-20T10:00:00Z" }),
    decision("tie-b", "run_failed", { created_at: "2026-11-20T10:00:00Z" }),
  ]);
  assert.deepEqual(
    rows.map((row) => row.id),
    ["new", "tie-a", "tie-b", "old"],
  );
});

test("空的 / 坏的时间不炸，也不参与排序翻转", () => {
  assert.deepEqual(supervisionRows(null), []);
  assert.deepEqual(supervisionRows([]), []);
  const rows = supervisionRows([
    decision("no-time", "run_failed", { created_at: "" }),
    decision("bad-time", "run_failed", { created_at: "not-a-date" }),
  ]);
  assert.equal(rows.length, 2);
  assert.equal(supervisionRowClock(""), "");
  assert.match(supervisionRowClock("2026-11-20T10:05:00Z"), /^\d{2}:\d{2}$/);
});

test("已知触发/动作/状态映射到 i18n 键，未知的回落 null 由视图显示原文", () => {
  assert.equal(supervisionTriggerKey("run_failed"), "task.supervision.trigger.run_failed");
  assert.equal(supervisionTriggerKey("direct_stalled"), "task.supervision.trigger.direct_stalled");
  assert.equal(supervisionTriggerKey("user_message"), null);
  assert.equal(supervisionActionKey("retry_direct"), "task.supervision.action.retry_direct");
  assert.equal(supervisionActionKey("escalate"), "task.supervision.action.escalate");
  assert.equal(supervisionActionKey("say"), null);
  assert.equal(supervisionStatusKey("applied"), "task.supervision.status.applied");
  assert.equal(supervisionStatusKey("shadow"), "task.supervision.status.shadow");
  assert.equal(supervisionStatusKey("bypassed"), null);
});

test("列表渲染：触发 → 动作 → 结果，错误单独一行", () => {
  const rows = supervisionRows([
    decision("d1", "direct_stalled", {
      action_kinds: ["retry_direct"],
      error: "reply act-1 is gone",
    }),
  ]);
  const html = renderToString(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(SupervisionEntryList, { rows }),
    ),
  );
  assert.match(html, /data-testid="supervision-list"/);
  assert.match(html, /data-testid="supervision-row-d1"/);
  assert.ok(html.includes(translateNow("task.supervision.trigger.direct_stalled")), "trigger label comes from i18n");
  assert.ok(html.includes(translateNow("task.supervision.action.retry_direct")), "action label comes from i18n");
  assert.ok(html.includes(translateNow("task.supervision.status.applied")), "status label comes from i18n");
  assert.match(html, /reply act-1 is gone/, "error text is shown verbatim");
});

test("源码契约：入口挂在运行视图之后、空时隐藏、取数走 appURL + nodeId", () => {
  const panel = read("../src/components/task/TaskDetailPanel.tsx");
  const runEntryAt = panel.indexOf("<RunViewEntry");
  const supervisionAt = panel.indexOf("<SupervisionEntry");
  assert.ok(runEntryAt >= 0 && supervisionAt > runEntryAt, "SupervisionEntry follows RunViewEntry in the detail panel");
  assert.match(
    panel,
    /^[ \t]*<SupervisionEntry rootId=\{task\.root_id\} taskId=\{task\.id\} nodeId=\{nodeId\} refreshKey=\{task\.updated_at\} \/>$/m,
    "the entry is mounted unconditionally with the same task identity props as the run view",
  );

  const entry = read("../src/components/task/SupervisionEntry.tsx");
  assert.match(entry, /rows\.length === 0/, "an empty supervision history hides the whole row");
  assert.match(entry, /data-testid="supervision-entry"/);

  const service = read("../src/services/task/routerDecisions.ts");
  assert.match(service, /appURL\(/, "requests must go through appURL so node routing stays explicit");
  assert.match(
    service,
    /router-decisions`, params, nodeId\)/,
    "requests must pass the node id to appURL (cross-node rule)",
  );
  assert.match(service, /router-decisions/);

  const list = read("../src/components/task/SupervisionEntryList.tsx");
  assert.doesNotMatch(list, /services\//, "view layer must not import services");
});
