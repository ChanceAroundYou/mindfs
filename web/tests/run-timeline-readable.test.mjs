import assert from "node:assert/strict";
import { test } from "vitest";
import React from "react";
import { renderToString } from "react-dom/server";
import { I18nProvider } from "../src/i18n/index.tsx";
import { RunTimeline } from "../src/components/task/RunTimeline.tsx";
import { nodeRunDurationMs, formatNodeRunDuration } from "../src/components/task/runTimelineModel.ts";

function renderTimeline(props) {
  return renderToString(
    React.createElement(I18nProvider, null, React.createElement(RunTimeline, props)),
  );
}

// 运行视图是给人看的“这次到底做了什么”，不是节点 JSON 转储。
// 真实样本（任务 #66）：output 只有 changes 字符串数组 + outcome + verification，
// 直接 JSON.stringify 出来就是用户说的“一堆莫名其妙的东西”。
//
// 任务 #87 第 3 条：节点输出里的自然语言段落（summary/result/message/text…）
// 就是 Router 回复已经写过一遍的原文，运行视图不再重复；只留回复里没有的东西 ——
// 变更清单、验证字段、耗时、尝试次数、错误。
test("run node renders only the readable result; prompts and raw JSON are gone", () => {
  const html = renderTimeline({
    nodeRuns: [
      {
        id: "nr-readable",
        run_id: "run-readable",
        node_id: "agent-1",
        attempt: 1,
        status: "success",
        input: { prompt: "把工作流页顶部的重复按钮去掉" },
        output: {
          changes: ["去掉与发布重复的按钮", "新增删除工作流入口"],
          outcome: "done",
          verification: { go_tests: "pass", web_tests: "616 passed" },
        },
      },
    ],
  });

  assert.doesNotMatch(html, /node-run-input-/, "input blocks are gone");
  assert.doesNotMatch(html, /把工作流页顶部的重复按钮去掉/, "prompts are not shown");
  assert.match(html, /data-testid="node-run-output-nr-readable"/, "output needs a readable block");
  assert.match(html, /去掉与发布重复的按钮/, "first change must be shown");
  assert.match(html, /新增删除工作流入口/, "second change must be shown");
  assert.match(html, />go_tests</, "verification keys must be readable");
  assert.match(html, />pass</, "verification values must be readable");
  assert.match(html, /616 passed/, "verification values must be readable");

  assert.doesNotMatch(html, /"changes"/, "raw JSON keys must not appear");
  assert.doesNotMatch(html, /"outcome"/, "envelope metadata must not appear");
  assert.doesNotMatch(html, /node-run-raw-/, "raw payload details are gone");
  assert.doesNotMatch(html, /原始数据|Raw data/, "no raw-data toggle remains");
});

test("empty metadata adds no readable rows and string input stays hidden", () => {
  const html = renderTimeline({
    nodeRuns: [
      {
        id: "nr-string",
        run_id: "run-string",
        node_id: "agent-2",
        attempt: 1,
        status: "success",
        input: "直接输入",
        output: { outcome: "done", reason: "" },
      },
    ],
  });
  assert.doesNotMatch(html, /直接输入/, "string input is no longer rendered");
  assert.doesNotMatch(html, /outcome/, "empty/metadata-only output adds no readable rows");
  assert.doesNotMatch(html, /reason/, "empty reason adds no readable rows");
});

// 任务 #87 第 3 条的核心：节点输出里的总结/结果/消息就是回复原文，再抄一遍
// 只是把同一段话在同一个面板里说两次。运行视图只该显示回复里看不到的东西。
test("prose output fields are not repeated inside the run view", () => {
  const replyText = "这一轮把会话页拆成了三个组件，并把运行视图入口换成了图标。";
  const html = renderTimeline({
    nodeRuns: [
      {
        id: "nr-prose",
        run_id: "run-prose",
        node_id: "agent-1",
        attempt: 1,
        status: "success",
        output: { summary: replyText, message: replyText, result: replyText },
      },
    ],
  });
  assert.doesNotMatch(html, /data-testid="node-run-output-nr-prose"/, "prose-only output adds no block");
  assert.doesNotMatch(html, /这一轮把会话页拆成了三个组件/);
  // 字符串形式的 output 同样是原文，不是运行视图该展示的内容。
  const stringHtml = renderTimeline({
    nodeRuns: [
      {
        id: "nr-str-output",
        run_id: "run-str-output",
        node_id: "agent-2",
        attempt: 1,
        status: "success",
        output: replyText,
      },
    ],
  });
  assert.doesNotMatch(stringHtml, /这一轮把会话页拆成了三个组件/);
});

// 运行视图要显示回复里没有的价值信息：耗时与第几次尝试。
test("run node shows duration and retry attempt instead of echoing the reply", () => {
  const html = renderTimeline({
    nodeRuns: [
      {
        id: "nr-timed",
        run_id: "run-timed",
        node_id: "agent-1",
        attempt: 3,
        status: "success",
        started_at: "2026-11-19T10:00:00Z",
        finished_at: "2026-11-19T10:07:05Z",
        output: { outcome: "done" },
      },
    ],
  });
  assert.match(html, /data-testid="node-run-duration-nr-timed"/, "duration must be rendered");
  assert.match(html, /7 分 5 秒|7m 5s/, "duration must be human readable");
  assert.match(html, /data-testid="node-run-attempt-nr-timed"/, "a retried node must say which attempt it is");
  assert.match(html, /第 3 次尝试|attempt 3/);
});

test("nodeRunDurationMs rejects missing, malformed and inverted timestamps", () => {
  assert.equal(nodeRunDurationMs({}), null);
  assert.equal(nodeRunDurationMs({ started_at: "2026-11-19T10:00:00Z" }), null);
  assert.equal(nodeRunDurationMs({ started_at: "not-a-date", finished_at: "2026-11-19T10:00:00Z" }), null);
  assert.equal(
    nodeRunDurationMs({ started_at: "2026-11-19T10:00:10Z", finished_at: "2026-11-19T10:00:00Z" }),
    null,
    "an end before the start is not a duration",
  );
  assert.equal(
    nodeRunDurationMs({ started_at: "2026-11-19T10:00:00Z", finished_at: "2026-11-19T10:00:02Z" }),
    2000,
  );
});

test("formatNodeRunDuration switches to minutes past the minute mark", () => {
  const t = (key, params) => `${key}:${JSON.stringify(params ?? null)}`;
  assert.equal(formatNodeRunDuration(0, t), `task.runTimeline.durationSeconds:{"n":1}`);
  assert.equal(formatNodeRunDuration(42_000, t), `task.runTimeline.durationSeconds:{"n":42}`);
  assert.equal(formatNodeRunDuration(125_000, t), `task.runTimeline.durationMinutes:{"m":2,"s":5}`);
});
