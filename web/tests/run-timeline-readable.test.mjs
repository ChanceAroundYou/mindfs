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
test("run node renders readable input/output and keeps raw JSON behind a toggle", () => {
  const html = renderTimeline({
    run: { id: "run-readable" },
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

  assert.match(html, /data-testid="node-run-input-nr-readable"/, "input needs a readable block");
  assert.match(html, /data-testid="node-run-output-nr-readable"/, "output needs a readable block");
  assert.match(html, /把工作流页顶部的重复按钮去掉/, "input prompt must be shown as text");
  assert.match(html, /去掉与发布重复的按钮/, "first change must be shown");
  assert.match(html, /新增删除工作流入口/, "second change must be shown");
  const readable = html.split('data-testid="node-run-raw-')[0];
  assert.match(readable, />go_tests</, "verification keys must be readable");
  assert.match(readable, />pass</, "verification values must be readable");
  assert.match(readable, /616 passed/, "verification values must be readable");

  assert.doesNotMatch(readable, /"changes"/, "raw JSON keys must not appear in the readable block");
  assert.doesNotMatch(readable, /"outcome"/, "envelope metadata must not appear in the readable block");
  assert.match(html, /<details[^>]*data-testid="node-run-raw-nr-readable"/, "raw payload stays available behind details");
});

test("string payloads remain readable and empty metadata does not add noise", () => {
  const html = renderTimeline({
    run: { id: "run-string" },
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
  assert.match(html, /直接输入/, "string input must stay visible");
  const readable = html.split('data-testid="node-run-raw-')[0];
  assert.doesNotMatch(readable, /outcome/, "empty/metadata-only output adds no readable rows");
  assert.doesNotMatch(readable, /reason/, "empty reason adds no readable rows");
});
