import assert from "node:assert/strict";
import { test } from "vitest";
import React from "react";
import { renderToString } from "react-dom/server";
import { I18nProvider } from "../src/i18n/index.tsx";
import { RunTimeline } from "../src/components/task/RunTimeline.tsx";

function renderTimeline(props) {
  return renderToString(
    React.createElement(I18nProvider, null, React.createElement(RunTimeline, props)),
  );
}

// 运行视图是给人看的“这次到底做了什么”，不是节点 JSON 转储。
// 真实样本（任务 #66）：output 只有 changes 字符串数组 + outcome + verification，
// 直接 JSON.stringify 出来就是用户说的“一堆莫名其妙的东西”。
// 简化后只保留结果本身：输入、原始 JSON、时间戳都不再渲染。
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
