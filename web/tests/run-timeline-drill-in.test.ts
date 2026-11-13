import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import React from "react";
import { renderToString } from "react-dom/server";
import { I18nProvider } from "../src/i18n/index.tsx";
import { RunTimeline } from "../src/components/task/RunTimeline.tsx";

const read = (p: string) => readFileSync(new URL(p, import.meta.url), "utf8");

function renderTimeline(props: React.ComponentProps<typeof RunTimeline>): string {
  return renderToString(
    React.createElement(
      I18nProvider,
      null,
      React.createElement(RunTimeline, props),
    ),
  );
}

test("run-timeline-drill-in", async () => {
  const component = read("../src/components/task/RunTimeline.tsx");
  const detailPanel = read("../src/components/task/TaskDetailPanel.tsx");

  // ── 层级契约：下钻子视图，不是平级 tab ──────────────────────────────
  // 组件自己声明层级：data-drill-in 标记 + 返回留言板的入口。没有返回口的“下钻”
  // 会被误当成又一级导航。
  assert.match(component, /data-drill-in="comment-run"/, "RunTimeline must declare it is a drill-in of a comment run");
  assert.match(component, /data-testid="run-timeline"/, "RunTimeline root must be identifiable");
  assert.match(component, /onBackToComments/, "drill-in must offer a way back to the comment board");
  assert.match(component, /task\.runTimeline\.backToComments/, "back navigation must be labelled via i18n");

  // 不得与留言板平级：a) 组件文件里不得出现 tab 语义（注释除外：说明层级的
  // 纪律性注释含“平级 tab”字样是允许的，断言只查代码）；b) TaskDetailPanel 不得
  // 把它 import 或挂成新的 tab（A2 未提交的 wiring 不在本 tranche 范围）。
  const componentCode = component
    .split("\n")
    .filter((line) => !line.trim().startsWith("//") && !line.trim().startsWith("*") && !line.trim().startsWith("/*"))
    .join("\n");
  assert.doesNotMatch(componentCode, /\btab\b/i, "RunTimeline must not present itself as a tab");
  assert.doesNotMatch(detailPanel, /RunTimeline/, "TaskDetailPanel must not wire RunTimeline yet (WP-07 tranche is standalone)");
  assert.doesNotMatch(detailPanel, /run-timeline/, "TaskDetailPanel must not reference the drill-in view");

  // ── 只读契约 ────────────────────────────────────────────────────────
  // 只读视图不得发起写操作：没有 fetch / POST / PUT / PATCH / DELETE。
  assert.doesNotMatch(component, /\bfetch\s*\(/, "read-only view must not fetch");
  assert.doesNotMatch(component, /method:\s*"(POST|PUT|PATCH|DELETE)"/, "read-only view must not mutate");
  // session 跳转走回调，不自己路由。
  assert.match(component, /onOpenSession/, "session jump must go through the onOpenSession callback");

  // ── 渲染：run + node run 状态/输入/输出/错误/session 跳转 ──────────────
  const html = renderTimeline({
    run: { id: "run-1", version: "wf1:v3", revision: 2 },
    nodeNames: { n1: "研究", n2: "汇总" },
    nodeRuns: [
      {
        id: "nr-1",
        run_id: "run-1",
        node_id: "n1",
        status: "success",
        agent: "claude",
        model: "opus",
        input: { query: "天气" },
        output: { answer: "晴" },
        session_key: "sess-abc",
        started_at: "2026-11-09T10:00:00Z",
        finished_at: "2026-11-09T10:01:00Z",
      },
      {
        id: "nr-2",
        run_id: "run-1",
        node_id: "n2",
        status: "fail",
        error: "超时",
      },
    ],
    onOpenSession: () => {},
    onBackToComments: () => {},
  });

  assert.match(html, /data-drill-in="comment-run"/, "rendered output must carry the drill-in marker");
  assert.match(html, /run-1/, "run id must be shown");
  assert.match(html, /wf1:v3/, "run version must be shown");
  assert.match(html, /研究/, "node display name must be used when provided");
  assert.match(html, /汇总/, "second node name must be shown");
  assert.match(html, /success/, "success status must be shown");
  assert.match(html, /fail/, "fail status must be shown");
  assert.match(html, /天气/, "node input must be shown");
  assert.match(html, /晴/, "node output must be shown");
  assert.match(html, /超时/, "node error must be shown");
  assert.match(html, /sess-abc|打开会话|Open session/, "session jump affordance must appear for nodes with a session_key");
  assert.match(html, /返回留言板|Back to comments/, "back-to-comments must be rendered when the callback is provided");
  assert.match(html, /data-testid="open-session-nr-1"/, "session button must be keyed by node run id");

  // 失败节点没有 session_key 时不得出现跳转按钮（避免点进去 404）。
  const noSessionHtml = renderTimeline({
    run: { id: "run-2" },
    nodeRuns: [{ id: "nr-3", node_id: "n9", status: "running" }],
    onOpenSession: () => {},
  });
  assert.doesNotMatch(noSessionHtml, /open-session-nr-3/, "node without session_key must not render a session button");
  assert.match(noSessionHtml, /n9/, "node_id fallback must be shown when no display name is provided");

  // 空态：没有 node run 时明确提示，不渲染空列表。
  const emptyHtml = renderTimeline({ run: { id: "run-3" }, nodeRuns: [] });
  assert.match(emptyHtml, /尚无节点执行记录|No node runs/, "empty node runs must show an explicit empty state");

  // Unknown 是「无法判定」，运行视图不得把它画成成功。
  const unknownHtml = renderTimeline({
    run: { id: "run-4" },
    nodeRuns: [{ id: "nr-unknown", node_id: "n4", status: "unknown", output: { outcome: "unknown" } }],
  });
  assert.match(unknownHtml, /data-status="unknown"/, "unknown node run must keep its own status");
  assert.doesNotMatch(unknownHtml, /data-status="success"/, "unknown must never be rendered as success");

  // 没有 run 时不崩。
  const noRunHtml = renderTimeline({ run: null, nodeRuns: [] });
  assert.match(noRunHtml, /暂无运行|No run yet/, "missing run must show an explicit state");

  // ── i18n 键齐全 ──────────────────────────────────────────────────────
  const zh = read("../src/i18n/locales/zh-CN-tasks.ts");
  const en = read("../src/i18n/locales/en-US-tasks.ts");
  for (const key of [
    "task.runTimeline.backToComments",
    "task.runTimeline.noRun",
    "task.runTimeline.empty",
    "task.runTimeline.input",
    "task.runTimeline.output",
    "task.runTimeline.error",
    "task.runTimeline.openSession",
  ]) {
    assert.ok(zh.includes(`"${key}"`), `zh-CN must define ${key}`);
    assert.ok(en.includes(`"${key}"`), `en-US must define ${key}`);
  }
});

// 运行视图入口是 human_gate 批准/驳回的唯一挂载点，必须出现在任务详情面板里。
// 面板同时只能有一条对话流：CommentThread 与 ConversationView 读的是同一份活动，
// 两个都挂就是「同一段对话出现两套」。
test("任务详情面板只挂一条对话流，且保留运行视图入口", () => {
  const panel = read("../src/components/task/TaskDetailPanel.tsx");
  const entry = read("../src/components/task/RunViewEntry.tsx");
  const hook = read("../src/components/task/useRunViewEntry.ts");

  assert.match(panel, /<ConversationView/, "TaskDetailPanel must mount the single conversation stream");
  assert.doesNotMatch(panel, /<CommentThread/, "CommentThread duplicates ConversationView and must not be mounted");
  assert.doesNotMatch(panel, /from "\.\/CommentThread"/, "no CommentThread import either");
  assert.match(panel, /<RunViewEntry/, "RunViewEntry is the only mount point of the human gate UI");
  assert.ok(
    panel.indexOf("<ConversationView") < panel.indexOf("<RunViewEntry"),
    "运行视图是对话流的下钻，必须排在它之后",
  );

  // 只有迁移到 workflow 引擎的任务才有运行视图；legacy 不渲染入口。
  // 引擎门槛在取数 hook（.ts）里：视图层不 import services。
  assert.match(hook, /engine\?\.engine === "workflow"/);
  assert.match(hook, /workflow_run_id/);
  assert.match(entry, /if \(!runId\) return null;/);
  // 收起即回到留言板（同层折叠，不跳页）。
  assert.ok(entry.includes("onBackToComments={collapse}"), "收起必须回到留言板（同一层级折叠）");
  // 取数必须显式传 nodeId（跨节点铁律：不传会静默打激活节点）。
  assert.ok(hook.includes("fetchTaskEngine(rootId, taskId, nodeId)"));
  assert.ok(hook.includes("fetchWorkflowRun(rootId, taskId, nodeId)"));
});
