import assert from "node:assert/strict";
import React from "react";
import { renderToString } from "react-dom/server";
import { test } from "vitest";
import { I18nProvider } from "../src/i18n/index.tsx";
import { zhCN } from "../src/i18n/locales/zh-CN.ts";
import { enUS } from "../src/i18n/locales/en-US.ts";
import { LegacyTaskView } from "../src/components/task/LegacyTaskView.tsx";
import { Collapsible } from "../src/components/task/Collapsible.tsx";
import {
  EMPTY_LEGACY_SUMMARY,
  extractSummaryFromStageRuns,
} from "../src/shared/legacySummary.ts";

// 仓库的 vitest 是 node 环境（没有 jsdom / testing-library），所以组件用
// renderToString 断言 HTML，交互态（折叠）靠受控 prop 分别渲染两遍。

const render = (node) => renderToString(React.createElement(I18nProvider, null, node));

function stageRun(overrides = {}) {
  return {
    id: "run-1",
    task_id: "task-1",
    stage_index: 1,
    stage_name: "Stage 1",
    role: "agent",
    status: "success",
    created_at: "2026-11-01T00:00:00Z",
    updated_at: "2026-11-01T00:00:00Z",
    ...overrides,
  };
}

function legacyDetail({ status = "success", stageRuns = [], stages, initialInput = "test" } = {}) {
  return {
    task: {
      id: "task-1",
      root_id: "root-1",
      task_template_id: "",
      task_template_name: "",
      current_stage_index: 1,
      status,
      created_at: "2026-11-01T00:00:00Z",
      updated_at: "2026-11-02T00:00:00Z",
      completed_at: "2026-11-02T00:00:00Z",
      stages: stages ?? [{ name: "Stage 1", role: "agent", prompt_template: initialInput }],
    },
    stage_runs: stageRuns,
    events: [],
  };
}

// ── 总结提取 ──────────────────────────────────────────────────────────────
test("extractSummaryFromStageRuns uses the last completed agent output", () => {
  const runs = [
    stageRun({ id: "r1", output: "第一轮输出" }),
    stageRun({ id: "r2", stage_index: 2, output: "  已修复 bug 并通过测试  " }),
  ];
  assert.equal(extractSummaryFromStageRuns(runs), "已修复 bug 并通过测试");
});

test("extractSummaryFromStageRuns ignores later failed runs and truncates long output", () => {
  const runs = [
    stageRun({ id: "r1", output: "x".repeat(600) }),
    stageRun({ id: "r2", stage_index: 2, status: "fail", output: "失败输出" }),
  ];
  const summary = extractSummaryFromStageRuns(runs);
  assert.equal(summary.length, 503);
  assert.ok(summary.endsWith("..."));
});

test("extractSummaryFromStageRuns falls back to the completed stage count", () => {
  // stage_runs 线上不落 agent 输出 —— 生产路径走计数降级，不能渲染空白。
  const runs = [
    stageRun({ id: "r1", status: "success" }),
    stageRun({ id: "r2", stage_index: 2, status: "fail" }),
  ];
  assert.equal(extractSummaryFromStageRuns(runs), "已完成 1/2 个阶段");
});

test("extractSummaryFromStageRuns handles empty stage_runs", () => {
  assert.equal(extractSummaryFromStageRuns([]), EMPTY_LEGACY_SUMMARY);
  assert.equal(extractSummaryFromStageRuns(null), EMPTY_LEGACY_SUMMARY);
  assert.equal(extractSummaryFromStageRuns(undefined, "自定义空文案"), "自定义空文案");
});

// ── LegacyTaskView ────────────────────────────────────────────────────────
test("LegacyTaskView renders the extracted summary and the initial input", () => {
  const html = render(
    React.createElement(LegacyTaskView, {
      detail: legacyDetail({
        status: "success",
        initialInput: "请实现用户登录功能",
        stageRuns: [stageRun({ output: "已修复 bug 并通过测试", session_key: "session-1" })],
      }),
      onClose: () => {},
      onOpenSession: () => {},
    }),
  );

  assert.match(html, /data-testid="legacy-summary-reply"/);
  assert.match(html, /已修复 bug 并通过测试/);
  assert.match(html, /请实现用户登录功能/);
  assert.match(html, /data-status="completed"/);
  assert.match(html, /打开会话|Open session/);
});

test("LegacyTaskView shows the no-record fallback for empty stage_runs", () => {
  const success = render(
    React.createElement(LegacyTaskView, { detail: legacyDetail({ status: "success" }), onClose: () => {} }),
  );
  assert.match(success, /任务已完成（无详细记录）/);

  // 失败/取消的任务不能说「已完成」—— 空记录文案要按终态区分。
  const failed = render(
    React.createElement(LegacyTaskView, { detail: legacyDetail({ status: "fail" }), onClose: () => {} }),
  );
  assert.match(failed, /任务已结束|Task finished/);
  assert.match(failed, /data-status="failed"/);
});

test("LegacyTaskView hides stages until the collapsible is opened", () => {
  const detail = legacyDetail({
    stages: [{ name: "历史阶段甲", role: "agent", prompt_template: "test" }],
    stageRuns: [],
  });

  const closed = render(
    React.createElement(LegacyTaskView, { detail, onClose: () => {} }),
  );
  assert.match(closed, /data-testid="legacy-stages-collapsible"/);
  assert.doesNotMatch(closed, /data-open="true"/);
  assert.doesNotMatch(closed, /历史阶段甲/);
  assert.match(closed, /查看执行详情|View execution details/);

  const opened = render(
    React.createElement(LegacyTaskView, { detail, onClose: () => {}, initialStagesOpen: true }),
  );
  assert.match(opened, /data-open="true"/);
  assert.match(opened, /历史阶段甲/);
  assert.match(opened, /此任务使用旧的阶段模式|legacy stage mode/);
});

// ── Collapsible ───────────────────────────────────────────────────────────
test("Collapsible renders children only when open", () => {
  const closed = render(
    React.createElement(Collapsible, { title: "标题", subtitle: "副标题", open: false, onToggle: () => {} },
      React.createElement("span", null, "child-body")),
  );
  assert.match(closed, /▶/);
  assert.doesNotMatch(closed, /▼/);
  assert.match(closed, /标题/);
  assert.match(closed, /副标题/);
  assert.doesNotMatch(closed, /child-body/);

  const opened = render(
    React.createElement(Collapsible, { title: "标题", open: true, onToggle: () => {} },
      React.createElement("span", null, "child-body")),
  );
  assert.match(opened, /▼/);
  assert.match(opened, /child-body/);
  assert.match(opened, /data-open="true"/);
});

// ── i18n ─────────────────────────────────────────────────────────────────
test("the legacy task i18n keys exist for both locales", () => {
  for (const key of [
    "task.legacy.viewStages",
    "task.legacy.stagesSubtitle",
    "task.legacy.noRecord",
  ]) {
    assert.equal(typeof zhCN[key], "string", `missing zh-CN key: ${key}`);
    assert.equal(typeof enUS[key], "string", `missing en-US key: ${key}`);
  }
});