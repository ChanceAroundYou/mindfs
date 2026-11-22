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

  // ── 层级契约：对话流的下钻子视图 ──────────────────────────────────────
  // 组件自己声明层级：data-drill-in 标记。返回留言板的按钮已删除——运行视图
  // 就挂在对话流里，去掉按钮不会让人迷路，反而少一个无意义控件。
  assert.match(component, /data-drill-in="comment-run"/, "RunTimeline must declare it is a drill-in of a comment run");
  assert.match(component, /data-testid="run-timeline"/, "RunTimeline root must be identifiable");
  assert.doesNotMatch(component, /onBackToComments|backToComments|run-timeline-back/, "the back-to-comments control is gone");
  assert.doesNotMatch(component, /run\.id|run\.version|run\.revision/, "the run id/version header is gone");

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

  // ── 渲染：节点状态/结果/错误/session 跳转（输入与原始 JSON 已删除） ─────
  const html = renderTimeline({
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
  });

  assert.match(html, /data-drill-in="comment-run"/, "rendered output must carry the drill-in marker");
  assert.match(html, /研究/, "node display name must be used when provided");
  assert.match(html, /汇总/, "second node name must be shown");
  assert.match(html, /data-status="success"/, "success status must be kept on the row");
  assert.match(html, /data-status="fail"/, "fail status must be kept on the row");
  assert.match(html, /晴/, "node result must be shown");
  assert.match(html, /超时/, "node error must be shown");
  assert.doesNotMatch(html, /天气/, "node input is no longer shown");
  assert.doesNotMatch(html, /run-1/, "run id is no longer shown");
  assert.doesNotMatch(html, /wf1:v3|r2/, "run version/revision are no longer shown");
  assert.match(html, /data-testid="open-session-nr-1"/, "session button must be keyed by node run id");
  assert.match(html, /打开会话|Open session/, "session jump affordance must appear for nodes with a session_key");
  assert.doesNotMatch(html, /返回留言板|Back to comments/, "the back-to-comments control is gone");

  // 失败节点没有 session_key 时不得出现跳转按钮（避免点进去 404）。
  const noSessionHtml = renderTimeline({
    nodeRuns: [{ id: "nr-3", node_id: "n9", status: "running" }],
    onOpenSession: () => {},
  });
  assert.doesNotMatch(noSessionHtml, /open-session-nr-3/, "node without session_key must not render a session button");
  assert.match(noSessionHtml, /n9/, "node_id fallback must be shown when no display name is provided");

  // 空态：没有 node run 时明确提示，不渲染空列表。
  const emptyHtml = renderTimeline({ nodeRuns: [] });
  assert.match(emptyHtml, /尚无节点执行记录|No node runs/, "empty node runs must show an explicit empty state");

  // Unknown 是「无法判定」，运行视图不得把它画成成功。
  const unknownHtml = renderTimeline({
    nodeRuns: [{ id: "nr-unknown", node_id: "n4", status: "unknown", output: { outcome: "unknown" } }],
  });
  assert.match(unknownHtml, /data-status="unknown"/, "unknown node run must keep its own status");
  assert.doesNotMatch(unknownHtml, /data-status="success"/, "unknown must never be rendered as success");

  // ── i18n 键齐全 ──────────────────────────────────────────────────────
  const zh = read("../src/i18n/locales/zh-CN-tasks.ts");
  const en = read("../src/i18n/locales/en-US-tasks.ts");
  for (const key of [
    "task.runTimeline.empty",
    "task.runTimeline.openSession",
  ]) {
    assert.ok(zh.includes(`"${key}"`), `zh-CN must define ${key}`);
    assert.ok(en.includes(`"${key}"`), `en-US must define ${key}`);
  }
  for (const key of [
    "task.runTimeline.backToComments",
    "task.runTimeline.noRun",
    "task.runTimeline.input",
    "task.runTimeline.output",
    "task.runTimeline.raw",
    "task.runTimeline.runLabel",
    "task.runTimeline.started",
    "task.runTimeline.finished",
    "task.runTimeline.error",
  ]) {
    assert.ok(!zh.includes(`"${key}"`), `zh-CN must drop ${key}`);
    assert.ok(!en.includes(`"${key}"`), `en-US must drop ${key}`);
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
  assert.match(panel, /<TaskInspectEntries/, "the inspect entries are the only mount point of the human gate UI");
  assert.ok(
    panel.indexOf("<ConversationView") < panel.indexOf("<TaskInspectEntries"),
    "运行视图是对话流的下钻，必须排在它之后",
  );

  // 迁移到 workflow 引擎的任务有运行视图；直连对话任务没有 WorkflowRun，
  // 但直连本身就是一种节点：按节点行展示并打开任务主会话，不写提示文案。
  // 引擎门槛在取数 hook（.ts）里：视图层不 import services。
  assert.match(hook, /engine\?\.engine === "workflow"/);
  assert.match(hook, /workflow_run_id/);
  assert.match(entry, /if \(!runId\) \{/);
  assert.match(entry, /fallbackSessionKey/);
  assert.match(entry, /data-testid="direct-node-run"/);
  assert.match(entry, /data-testid="direct-node-open-session"/);
  assert.doesNotMatch(entry, /directHint/, "直连不再写提示文案");
  // 两个胶囊同排、始终存在、互斥展开，且各自带人字箭头 icon（展开时旋转）。
  const inspect = read("../src/components/task/TaskInspectEntries.tsx");
  assert.match(inspect, /testId="run-view-toggle"/);
  assert.match(inspect, /testId="supervision-toggle"/);
  assert.match(inspect, /aria-expanded=\{open\}/);
  assert.match(inspect, /<ChevronIcon open=\{open\} \/>/);
  assert.match(
    inspect,
    /setOpenSection\(\(current\) => \(current === section \? null : section\)\)/,
    "一个 openSection 状态保证两个面板互斥",
  );
  // 胶囊行在面板正文里的位置由 DOM 流决定，两端同一顺序（对话流 → 胶囊行 →
  // 收尾区）。移动端曾用 `order:-1` 把它提到最前面，用户两次复测报「吸顶」：
  // 它恒在正文顶端、贴着面板标题下沿。可见性改由「首屏滚到底 + 输入区贴底
  // 对齐」保证，所以这里连 order 覆盖本身都不许存在。
  assert.match(panel, /className="task-detail-execution"/);
  const css = read("../src/index.css");
  assert.doesNotMatch(css, /\.task-detail-execution \{\s*order:/);
  // 运行视图就在对话流里，不再提供「返回留言板」的折叠入口。
  assert.doesNotMatch(entry, /onBackToComments/, "the back-to-comments wiring is gone");
  // 取数必须显式传 nodeId（跨节点铁律：不传会静默打激活节点）。
  assert.ok(hook.includes("fetchTaskEngine(rootId, taskId, nodeId)"));
  // 运行视图的数据源是一个端点：它按 Run 分组返回所有轮次（含每轮节点行），
  // 不再先取单条 run 再取节点行 —— 那样只能看到引擎绑定的最新一轮。
  assert.ok(hook.includes("fetchWorkflowNodeRuns(rootId, taskId, nodeId)"));
  // 引擎绑定在对话回合里才会从 legacy 变成 workflow；入口只在挂载时探测一次，
  // 新建任务当场跑完 workflow 也永远看不到运行视图。任务头更新时要重新探测。
  assert.match(panel, /refreshKey=\{task\.updated_at\}/, "task updates must re-probe the engine");
  assert.match(hook, /refreshKey/, "the hook must react to the refresh key");
});
