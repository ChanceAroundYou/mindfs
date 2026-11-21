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
  canSendActivity,
  normalizeActivityStatus,
} from "../src/shared/activityModel.ts";
import { workflowDisplayName } from "../src/shared/workflowDisplay.ts";

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

test("canSendActivity enforces empty body, specified workflow and in-flight guards", () => {
  assert.equal(canSendActivity({ body: "", workflowChoice: "empty" }), false);
  assert.equal(canSendActivity({ body: "   ", workflowChoice: "empty" }), false);
  assert.equal(canSendActivity({ body: "hi", workflowChoice: "specified", workflowVersionId: "" }), false);
  assert.equal(canSendActivity({ body: "hi", workflowChoice: "specified", workflowVersionId: "v1" }), true);
  assert.equal(canSendActivity({ body: "hi", workflowChoice: "disabled" }), true);
  assert.equal(canSendActivity({ body: "hi", workflowChoice: "empty", sending: true }), false);
  // 带附件时允许空正文（与会话输入框一致：粘贴一张图不配文字也能发）。
  assert.equal(canSendActivity({ body: "", workflowChoice: "empty", hasAttachments: true }), true);
  assert.equal(canSendActivity({ body: "   ", workflowChoice: "empty", hasAttachments: true }), true);
  assert.equal(canSendActivity({ body: "", workflowChoice: "specified", workflowVersionId: "", hasAttachments: true }), false);
});

test("activityDisplayText prefers the summary for terminal states", () => {
  assert.equal(activityDisplayText({ ...baseActivity, status: "running", body: "步骤 1/3: 分析" }), "步骤 1/3: 分析");
  assert.equal(activityDisplayText({ ...baseActivity, status: "completed", summary: "总结", body: "步骤" }), "总结");
  assert.equal(activityDisplayText({ ...baseActivity, status: "failed", summary: "", body: "错误" }), "错误");
  assert.equal(activityDisplayText({ ...baseActivity, status: "pending", summary: "备用", body: "" }), "备用");
  assert.equal(activityDisplayText({ ...baseActivity, status: "waiting_user", summary: "等待你处理：缺权限", body: "步骤 1/2" }), "等待你处理：缺权限");
});

test("normalizeActivityStatus defaults unknown/empty to pending", () => {
  assert.equal(normalizeActivityStatus("completed"), "completed");
  assert.equal(normalizeActivityStatus("failed"), "failed");
  assert.equal(normalizeActivityStatus("waiting_user"), "waiting_user");
  assert.equal(normalizeActivityStatus(""), "pending");
  assert.equal(normalizeActivityStatus("bogus"), "pending");
});

// ── 组件：状态显示（Review Focus #4 的状态面） ─────────────────────────────
test("workflowDisplayName resolves the friendly name and never leaks the version id", () => {
  assert.equal(workflowDisplayName("workflow-blank:v1", { "workflow-blank": "空白执行" }), "空白执行");
  assert.equal(workflowDisplayName("workflow-blank:v12", { "workflow-blank": "空白执行" }), "空白执行");
  assert.equal(workflowDisplayName("wf-feature:v3", {}), "");
  assert.equal(workflowDisplayName("", { "workflow-blank": "空白执行" }), "");
});

test("ActivityMessage renders a user message on the user side", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: { ...baseActivity, type: "user_message", role: "user", author: "用户", body: "帮我修登录" },
    }),
  );
  assert.match(html, /data-testid="activity-user"/);
  assert.match(html, /data-activity-id="a1"/);
  assert.match(html, /帮我修登录/);
  // 复用会话界面的用户气泡：右对齐、灰底、大圆角，不再有角色标签/头像。
  assert.match(html, /rgba\(148,163,184,0\.14\)/);
  assert.doesNotMatch(html, /👤|🤖/);
  assert.doesNotMatch(html, />用户<|>User</);
});

test("ActivityMessage keeps the reply footer but drops the message-level status", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: { ...baseActivity, status: "running", body: "步骤 2/4: 写测试", run_id: "run-1" },
    }),
  );
  assert.match(html, /data-testid="activity-agent"/);
  assert.match(html, /data-activity-id="a1"/);
  assert.match(html, /步骤 2\/4: 写测试/);
  assert.match(html, /data-testid="activity-footer"/);
  // 状态只保留在任务详情头部，消息页脚不再重复渲染。
  assert.doesNotMatch(html, /执行中|Running/);
  assert.doesNotMatch(html, /🤖|⚡/);
  assert.doesNotMatch(html, />Agent</);
});

test("ActivityMessage renders the summary for completed replies without a status chip", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: { ...baseActivity, status: "completed", body: "步骤 4/4: 收尾", summary: "已修复登录并补了测试", run_id: "run-1" },
    }),
  );
  assert.match(html, /data-testid="activity-agent"/);
  assert.match(html, /data-activity-id="a1"/);
  assert.match(html, /已修复登录并补了测试/);
  assert.match(html, /data-testid="activity-footer"/);
  assert.doesNotMatch(html, /已完成|Completed/);
  assert.doesNotMatch(html, /✓/);
});

test("ActivityMessage renders failed replies as errors without a status chip", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: { ...baseActivity, status: "failed", summary: "工作流校验失败", run_id: "run-1" },
    }),
  );
  assert.match(html, /data-testid="activity-agent"/);
  assert.match(html, /data-activity-id="a1"/);
  assert.match(html, /工作流校验失败/);
  assert.doesNotMatch(html, /var\(--status-bad\)/);
  assert.doesNotMatch(html, /✗/);
});

test("ActivityMessage keeps cancelled replies visible without pretending success", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: { ...baseActivity, status: "cancelled", summary: "用户取消", run_id: "run-1" },
    }),
  );
  assert.match(html, /data-testid="activity-agent"/);
  assert.match(html, /data-activity-id="a1"/);
  assert.match(html, /用户取消/);
  assert.doesNotMatch(html, /已取消|Cancelled/);
  assert.doesNotMatch(html, /○/);
});

test("ActivityMessage footer shows the friendly workflow name, not the version id", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: {
        ...baseActivity,
        status: "running",
        body: "执行中",
        workflow_version_id: "workflow-blank:v1",
        created_at: "2026-11-16T08:00:00Z",
      },
      workflowNames: { "workflow-blank": "空白执行" },
    }),
  );
  assert.match(html, /data-testid="activity-footer"/);
  assert.match(html, /空白执行/);
  assert.doesNotMatch(html, /workflow-blank/);
  assert.doesNotMatch(html, /:v1/);
});

test("ActivityMessage offers the session action in the footer when the reply carries one", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: { ...baseActivity, status: "completed", summary: "ok", run_id: "run-1", session_key: "s-1" },
      onOpenSession: () => {},
    }),
  );
  assert.match(html, /data-testid="activity-footer"[\s\S]*data-testid="activity-open-session"/);
  assert.match(html, /打开会话|Open session/);
  // 运行详情就在面板下方（RunViewEntry），消息里不再另设入口。
  assert.doesNotMatch(html, /data-testid="activity-view-run"/);
});

test("ActivityMessage renders system events as a neutral line", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: { ...baseActivity, type: "system_event", role: "system", body: "工作流已切换" },
    }),
  );
  assert.match(html, /data-testid="activity-system"/);
  assert.match(html, /data-activity-id="a1"/);
  assert.match(html, /工作流已切换/);
});

test("ActivityMessage localizes the placeholder when the backend sends no body", () => {
  // 后端只写内容不写壳：pending/running 的正文提示必须由前端按 locale 渲染，
  // 英文界面不能出现「正在思考...」这种硬编码中文。
  const pending = render(
    React.createElement(ActivityMessage, {
      activity: { ...baseActivity, status: "pending", body: "" },
    }),
  );
  assert.match(pending, /等待回复|Waiting for a reply/);

  const running = render(
    React.createElement(ActivityMessage, {
      activity: { ...baseActivity, status: "running", body: "" },
    }),
  );
  assert.match(running, /正在回复|Replying/);
});

test("ActivityMessage leaves terminal replies quiet instead of inventing a status", () => {
  const html = render(
    React.createElement(ActivityMessage, {
      activity: { ...baseActivity, status: "completed", body: "", summary: "" },
    }),
  );
  assert.match(html, /data-testid="activity-footer"/);
  // 消息级状态已收敛到任务头部：终态空正文既不补占位、也不亮状态。
  assert.doesNotMatch(html, /已完成|Completed|已取消|Cancelled|失败|Failed/);
  assert.doesNotMatch(html, /正在回复|等待回复|Replying|Waiting for a reply/);
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
  assert.match(html, /data-testid="activity-user"/);
  assert.match(html, /data-testid="activity-agent"/);
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
  // 页脚的工作流友好名来自控制面列表；视图层只能经 colocated hook 取数。
  assert.match(view, /useWorkflowNames\(\)/);
  assert.match(view, /workflowNames=\{workflowNames\}/);
  const hook = read("components/task/useWorkflowNames.ts");
  assert.match(hook, /listAvailableWorkflows\(/);
});

test("the activity i18n keys exist for both locales", () => {
  for (const key of [
    "task.activity.empty",
    "task.activity.inputPlaceholder",
    "task.activity.send",
    "task.activity.sending",
    "task.activity.openSession",
    "task.activity.status.completed",
    "task.activity.replyPending",
    "task.activity.replyRunning",
    "task.activity.loadFailed",
  ]) {
    assert.equal(typeof zhCN[key], "string", `missing zh-CN key: ${key}`);
  }
  // 角色标签与快捷键提示是纯噪音，已随对话 UI 重建移除。
  for (const key of ["task.activity.user", "task.activity.agent", "task.activity.sendHint"]) {
    assert.equal(zhCN[key], undefined, `removed zh-CN key still exists: ${key}`);
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

test("TaskDetailPanel always renders ConversationView; the legacy branch is gone", () => {
  const panel = read("components/task/TaskDetailPanel.tsx");
  assert.match(panel, /<ConversationView/, "every task, including converted history, renders the conversation view");
  // 同一份 /activities 只能挂一套「列表 + 输入框」：旧 CommentThread 与 ConversationView
  // 并存时，界面上会出现两份对话流、两个工作流选择器（线上旧产物踩过）。
  assert.equal(
    (panel.match(/<ConversationView/g) || []).length,
    1,
    "TaskDetailPanel must render exactly one conversation flow",
  );
  assert.doesNotMatch(panel, /import[^\n]*CommentThread/, "the legacy comment thread must not be imported");
  assert.doesNotMatch(panel, /<CommentThread/, "the legacy comment thread must not be mounted next to ConversationView");
  assert.doesNotMatch(panel, /<ActivityInput/, "the input belongs to ConversationView, not the panel");
  assert.doesNotMatch(panel, /WorkflowSelector/, "the workflow selector belongs to ConversationView's input");
  assert.doesNotMatch(panel, /LegacyTaskView/, "no compatibility branch may survive the stage->conversation conversion");
  assert.doesNotMatch(panel, /hasLegacyStages/, "the detail panel must not branch on stage snapshots");
  assert.doesNotMatch(panel, /task_stages_json/, "the stage snapshot marker is no longer a frontend concept");

  const view = read("components/task/ConversationView.tsx");
  assert.equal(
    (view.match(/<ActivityInput/g) || []).length,
    1,
    "ConversationView must mount exactly one input",
  );
});

test("the panel is keyed by task identity so drafts cannot leak into another task", () => {
  const gate = read("components/app/TaskDetailPanelGate.tsx");
  // 草稿/工作流选择/运行视图展开态都是任务级本地状态；没有 key 时切任务会复用实例，
  // 在 A 里写了一半的留言会被发到 B。键里必须带 root_id：任务 id 只在各自项目内唯一。
  assert.match(
    gate,
    /key=\{`\$\{selectedKanbanTask\._nodeId \|\| ""\}::\$\{selectedKanbanTask\.root_id \|\| ""\}::\$\{selectedKanbanTask\.id\}`\}/,
    "TaskDetailPanel must remount per (node, root_id, task id) —— 同名项目的两张卡也是两条身份",
  );
});

test("Markdown content shrinks inside the mobile task panel instead of forcing horizontal scroll", () => {
  const hook = read("hooks/useMarkdownViewer.tsx");
  // 宽表是移动端横向滚动的主因：520px 硬下限会把表格顶出卡片。
  assert.match(hook, /minWidth: "min\(520px, 100%\)"/);
  assert.doesNotMatch(hook, /minWidth: "520px"/);
  assert.doesNotMatch(hook, /whiteSpace: "nowrap"/);
  assert.match(hook, /overflowWrap: "anywhere"/);
  const view = read("components/task/ConversationView.tsx");
  assert.match(view, /minWidth: 0, maxWidth: "100%"/);
});
