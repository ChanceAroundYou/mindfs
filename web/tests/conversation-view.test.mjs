import assert from "node:assert/strict";
import fs from "./source-map-fs.mjs";
import path from "node:path";
import ts from "typescript";
import vm from "node:vm";
import React from "react";
import { renderToString } from "react-dom/server";
import { test } from "vitest";
import { I18nProvider } from "../src/i18n/index.tsx";
import { zhCN } from "../src/i18n/locales/zh-CN.ts";
import { ActivityMessage } from "../src/components/task/ActivityMessage.tsx";
import { ActivityInput } from "../src/components/task/ActivityInput.tsx";
import { ConversationMessages } from "../src/components/task/ConversationView.tsx";
import {
  activityDisplayText,
  applyActivityCreated,
  applyActivityUpdated,
  canSendActivity,
  normalizeActivityStatus,
} from "../src/shared/activityModel.ts";

const SRC = path.resolve(import.meta.dirname, "../src");
const read = (rel) => fs.readFileSync(path.join(SRC, rel), "utf8");
const render = (node) => renderToString(React.createElement(I18nProvider, null, node));

// ── 服务层：端点/参数/错误透传 ─────────────────────────────────────────────
// 组件层禁止 import services（reviewer-architecture-rules.test.mjs），所以数据层
// 的行为只能在这里单独验证：URL 必须带 root_id（后端缺它会 400），POST 必须把
// body 与 workflow_choice 一起发出去，且错误文案要能原样回到调用方。
function loadActivityService({ response, reject } = {}) {
  const file = path.join(SRC, "services/activities.ts");
  const compiled = ts.transpileModule(fs.readFileSync(file, "utf8"), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  const calls = [];
  const sandbox = {
    exports: {},
    module: { exports: {} },
    console,
    URLSearchParams,
    require: (name) => {
      if (name.includes("net/base")) {
        return {
          appURL: (p, params, nodeId) => {
            const query = params ? `?${params.toString()}` : "";
            calls.push({ kind: "path", path: p, query, nodeId });
            return `/app${p}${query}`;
          },
        };
      }
      if (name.includes("net/api")) {
        return {
          protectedJSON: async (url, init) => {
            calls.push({ kind: "request", url, init });
            if (reject) throw reject;
            return response;
          },
        };
      }
      throw new Error(`unexpected require: ${name}`);
    },
  };
  vm.runInNewContext(compiled, sandbox, { filename: file });
  return { service: sandbox.exports, calls };
}

test("listActivities scopes the request to the root and forwards the node", async () => {
  const { service, calls } = loadActivityService({ response: { activities: [{ id: "a1" }] } });

  const items = await service.listActivities("task-1", "root-1", "node-9");

  assert.equal(calls[0].kind, "path");
  assert.equal(calls[0].path, "/api/tasks/task-1/activities");
  assert.equal(new URLSearchParams(calls[0].query).get("root_id"), "root-1");
  assert.equal(calls[0].nodeId, "node-9");
  assert.equal(calls[1].url, "/app/api/tasks/task-1/activities?root_id=root-1");
  assert.equal(JSON.stringify(items), JSON.stringify([{ id: "a1" }]));
});

test("listActivities rejects a missing root instead of letting the backend 400", async () => {
  const { service } = loadActivityService({ response: { activities: [] } });
  await assert.rejects(() => service.listActivities("task-1", ""), /root_id required/);
});

test("createActivity posts body + workflow choice + root", async () => {
  const { service, calls } = loadActivityService({
    response: { user_activity: { id: "u1" }, agent_activity: { id: "a1" } },
  });

  const result = await service.createActivity(
    "task-1",
    "root-1",
    { body: "修一下登录", workflow_choice: "specified", workflow_version_id: "v3" },
    "node-2",
  );

  const request = calls.find((call) => call.kind === "request");
  assert.equal(request.url, "/app/api/tasks/task-1/activities");
  assert.equal(String(request.init.method).toUpperCase(), "POST");
  assert.equal(request.init.headers["Content-Type"], "application/json");
  assert.equal(
    JSON.stringify(JSON.parse(request.init.body)),
    JSON.stringify({ root_id: "root-1", body: "修一下登录", workflow_choice: "specified", workflow_version_id: "v3" }),
  );
  assert.equal(result.user_activity.id, "u1");
});

test("createActivity surfaces the backend error text", async () => {
  const { service } = loadActivityService({ reject: new Error("body cannot be empty") });
  await assert.rejects(
    () => service.createActivity("task-1", "root-1", { body: " ", workflow_choice: "empty" }),
    /body cannot be empty/,
  );
});

test("getActivity includes both ids and the root scope", async () => {
  const { service, calls } = loadActivityService({ response: { id: "a1" } });
  const activity = await service.getActivity("task-1", "a1", "root-1");
  assert.equal(calls[0].kind, "path");
  assert.equal(calls[0].path, "/api/tasks/task-1/activities/a1");
  assert.equal(new URLSearchParams(calls[0].query).get("root_id"), "root-1");
  assert.equal(activity.id, "a1");
});

// ── 活动流合并：WS 增量 + 重连补偿 ─────────────────────────────────────────
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

test("applyActivityCreated appends new activities and replaces duplicate echoes", () => {
  const first = applyActivityCreated([], baseActivity);
  assert.equal(first.length, 1);

  const duplicated = applyActivityCreated(first, { ...baseActivity, body: "重新投递" });
  assert.equal(duplicated.length, 1);
  assert.equal(duplicated[0].body, "重新投递");
});

test("applyActivityUpdated merges status/summary into the matching activity only", () => {
  const other = { ...baseActivity, id: "a2" };
  const list = [baseActivity, other];
  const next = applyActivityUpdated(list, "a1", { status: "completed", summary: "done" });

  assert.equal(next[0].status, "completed");
  assert.equal(next[0].summary, "done");
  assert.equal(next[0].body, baseActivity.body);
  assert.equal(next[1], other);
});

test("applyActivityUpdated returns the same array when the id is unknown", () => {
  const list = [baseActivity];
  assert.equal(applyActivityUpdated(list, "missing", { status: "failed" }), list);
});

test("canSendActivity enforces empty body, specified workflow and in-flight guards", () => {
  assert.equal(canSendActivity({ body: "", workflowChoice: "empty" }), false);
  assert.equal(canSendActivity({ body: "   ", workflowChoice: "empty" }), false);
  assert.equal(canSendActivity({ body: "hi", workflowChoice: "specified", workflowVersionId: "" }), false);
  assert.equal(canSendActivity({ body: "hi", workflowChoice: "specified", workflowVersionId: "v1" }), true);
  assert.equal(canSendActivity({ body: "hi", workflowChoice: "disabled" }), true);
  assert.equal(canSendActivity({ body: "hi", workflowChoice: "empty", sending: true }), false);
  assert.equal(canSendActivity({ body: "hi", workflowChoice: "empty", disabled: true }), false);
});

test("activityDisplayText prefers the summary for terminal states", () => {
  assert.equal(activityDisplayText({ ...baseActivity, status: "running", body: "步骤 1/3: 分析" }), "步骤 1/3: 分析");
  assert.equal(activityDisplayText({ ...baseActivity, status: "completed", summary: "总结", body: "步骤" }), "总结");
  assert.equal(activityDisplayText({ ...baseActivity, status: "failed", summary: "", body: "错误" }), "错误");
  assert.equal(activityDisplayText({ ...baseActivity, status: "pending", summary: "备用", body: "" }), "备用");
});

test("normalizeActivityStatus defaults unknown/empty to pending", () => {
  assert.equal(normalizeActivityStatus("completed"), "completed");
  assert.equal(normalizeActivityStatus("failed"), "failed");
  assert.equal(normalizeActivityStatus(""), "pending");
  assert.equal(normalizeActivityStatus("bogus"), "pending");
});

// ── 组件：状态显示（Review Focus #4 的状态面） ─────────────────────────────
test("ActivityMessage renders a user message on the user side", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: { ...baseActivity, type: "user_message", role: "user", author: "用户", body: "帮我修登录" },
    }),
  );
  assert.match(html, /data-testid="activity-message"/);
  assert.match(html, /data-role="user"/);
  assert.match(html, /帮我修登录/);
  assert.doesNotMatch(html, /data-status=/);
});

test("ActivityMessage shows the running status and live progress body", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: { ...baseActivity, status: "running", body: "步骤 2/4: 写测试" },
    }),
  );
  assert.match(html, /data-role="agent"/);
  assert.match(html, /data-status="running"/);
  assert.match(html, /步骤 2\/4: 写测试/);
  assert.match(html, /执行中|Running/);
});

test("ActivityMessage renders the summary for completed replies", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: { ...baseActivity, status: "completed", body: "步骤 4/4: 收尾", summary: "已修复登录并补了测试" },
    }),
  );
  assert.match(html, /data-status="completed"/);
  assert.match(html, /已修复登录并补了测试/);
  assert.match(html, /已完成|Completed/);
});

test("ActivityMessage renders failed replies as errors", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: { ...baseActivity, status: "failed", summary: "工作流校验失败" },
    }),
  );
  assert.match(html, /data-status="failed"/);
  assert.match(html, /工作流校验失败/);
  assert.match(html, /失败|Failed/);
  assert.match(html, /#dc2626/);
});

test("ActivityMessage keeps cancelled replies visible without pretending success", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: { ...baseActivity, status: "cancelled", summary: "用户取消" },
    }),
  );
  assert.match(html, /data-status="cancelled"/);
  assert.match(html, /用户取消/);
  assert.match(html, /已取消|Cancelled/);
});

test("ActivityMessage offers detail and session actions when the reply carries them", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: { ...baseActivity, status: "completed", summary: "ok", run_id: "run-1", session_key: "s-1" },
      onViewRun: () => {},
      onOpenSession: () => {},
    }),
  );
  assert.match(html, /查看执行详情|View run details/);
  assert.match(html, /打开会话|Open session/);
});

test("ActivityMessage renders system events as a neutral line", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: { ...baseActivity, type: "system_event", role: "system", body: "工作流已切换" },
    }),
  );
  assert.match(html, /data-role="system"/);
  assert.match(html, /工作流已切换/);
});

// ── 组件：输入框（Review Focus #1 的前端半边） ─────────────────────────────
const inputProps = {
  body: "",
  onBodyChange: () => {},
  choice: "empty",
  versionId: "",
  onWorkflowChange: () => {},
  onSend: () => {},
};

test("ActivityInput disables send when the body is empty", () => {
  const html = render(React.createElement(ActivityInput, inputProps));
  assert.match(html, /data-testid="activity-input"/);
  assert.match(html, /data-testid="activity-send"[^>]*disabled/);
});

test("ActivityInput enables send when the body has text", () => {
  const html = render(React.createElement(ActivityInput, { ...inputProps, body: "test message" }));
  assert.doesNotMatch(html, /data-testid="activity-send"[^>]*disabled/);
});

test("ActivityInput blocks a specified workflow without a version", () => {
  const html = render(
    React.createElement(ActivityInput, { ...inputProps, body: "test", choice: "specified", versionId: "" }),
  );
  assert.match(html, /data-testid="activity-send"[^>]*disabled/);
});

test("ActivityInput renders the API error and sending state", () => {
  const html = render(
    React.createElement(ActivityInput, { ...inputProps, body: "test", error: "body cannot be empty", sending: true }),
  );
  assert.match(html, /data-testid="input-error"/);
  assert.match(html, /body cannot be empty/);
  assert.match(html, /发送中|Sending/);
});

// ── 组件：对话流容器 ───────────────────────────────────────────────────────
test("ConversationMessages renders user and agent messages in order", () => {
  const html = render(
    React.createElement(ConversationMessages, {
      activities: [
        { ...baseActivity, id: "u1", type: "user_message", role: "user", author: "用户", body: "做完了吗？", status: undefined },
        { ...baseActivity, id: "a1", status: "completed", summary: "已经完成" },
      ],
    }),
  );
  assert.match(html, /data-testid="conversation-messages"/);
  assert.ok(html.indexOf("做完了吗？") < html.indexOf("已经完成"));
  assert.match(html, /data-status="completed"/);
});

test("ConversationMessages shows empty, loading and error states", () => {
  const empty = render(React.createElement(ConversationMessages, { activities: [] }));
  assert.match(empty, /data-testid="conversation-empty"/);
  assert.match(empty, /暂无对话|No messages yet/);

  const loading = render(React.createElement(ConversationMessages, { activities: [], loading: true }));
  assert.match(loading, /data-testid="conversation-loading"/);

  const error = render(
    React.createElement(ConversationMessages, { activities: [], error: "HTTP 500", onReload: () => {} }),
  );
  assert.match(error, /data-testid="conversation-error"/);
  assert.match(error, /HTTP 500/);
  assert.match(error, /重试|Retry/);
});

// ── 接线守卫：hook / WS / 组件数据源 ───────────────────────────────────────
test("useTaskActivities wires the REST load, WS increments and reconnect catch-up", () => {
  const hook = read("hooks/useTaskActivities.ts");
  assert.match(hook, /listActivities\(/);
  assert.match(hook, /createActivity\(/);
  assert.match(hook, /task\.activity\.created/);
  assert.match(hook, /task\.activity\.updated/);
  assert.match(hook, /ws\.reconnected/);
  assert.match(hook, /applyActivityCreated/);
  assert.match(hook, /applyActivityUpdated/);
  assert.match(hook, /subscribeEvents/);
});

test("ConversationView drives the messages from useTaskActivities", () => {
  const view = read("components/task/ConversationView.tsx");
  assert.match(view, /useTaskActivities\(\s*taskId,\s*rootId,\s*nodeId,?\s*\)/);
  assert.match(view, /<ConversationMessages/);
  assert.match(view, /<ActivityInput/);
});

test("the activity i18n keys exist for both locales", () => {
  for (const key of [
    "task.activity.user",
    "task.activity.agent",
    "task.activity.empty",
    "task.activity.inputPlaceholder",
    "task.activity.send",
    "task.activity.sending",
    "task.activity.viewRun",
    "task.activity.openSession",
    "task.activity.status.pending",
    "task.activity.status.running",
    "task.activity.status.completed",
    "task.activity.status.failed",
    "task.activity.status.cancelled",
    "task.activity.loadFailed",
  ]) {
    assert.equal(typeof zhCN[key], "string", `missing zh-CN key: ${key}`);
  }
});

// ── TaskDetailPanel 集成守卫 ─────────────────────────────────────────────
test("TaskDetailPanel integrates ConversationView with the right props", () => {
  const panel = read("components/task/TaskDetailPanel.tsx");
  assert.match(panel, /ConversationView/, "TaskDetailPanel must import and render ConversationView");
  assert.match(panel, /taskId=\{task\.id\}/, "must pass task.id as taskId");
  assert.match(panel, /rootId=\{task\.root_id\}/, "must pass task.root_id as rootId");
  assert.match(panel, /disabled=\{terminal\}/, "must disable input for terminal tasks");
  assert.match(panel, /onOpenSession=\{onOpenSession\}/, "must wire onOpenSession through");
});

test("TaskDetailPanel does not import from services directly", () => {
  const panel = read("components/task/TaskDetailPanel.tsx");
  assert.doesNotMatch(panel, /from ["'][^"']*services\/activities/, "components must not import activities service directly");
});