import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "vitest";
import React from "react";
import { renderToString } from "react-dom/server";
import { I18nProvider } from "../src/i18n/index.tsx";
import { RunTimeline } from "../src/components/task/RunTimeline.tsx";
import { runNodeKinds, isWaitingHumanGate } from "../src/components/task/runTimelineModel.ts";

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

function renderTimeline(props) {
  return renderToString(
    React.createElement(I18nProvider, null, React.createElement(RunTimeline, props)),
  );
}

// 网关动作只认「HumanGate 节点 + waiting_user」这一种组合；节点类型来自 run 快照
// 的 graph，NodeRun 自己不带 kind。
test("runNodeKinds maps graph node ids to kinds and tolerates partial graphs", () => {
  assert.deepEqual(
    runNodeKinds({ nodes: [{ id: "g", kind: "human_gate" }, { id: "a", kind: "agent" }, { id: "" }] }),
    { g: "human_gate", a: "agent" },
  );
  assert.deepEqual(runNodeKinds(null), {});
  assert.deepEqual(runNodeKinds({}), {});
});

test("isWaitingHumanGate is true only for a waiting human_gate node run", () => {
  const kinds = { g: "human_gate", a: "agent" };
  assert.equal(isWaitingHumanGate({ node_id: "g", status: "waiting_user" }, kinds), true);
  assert.equal(isWaitingHumanGate({ node_id: "g", status: "success" }, kinds), false);
  assert.equal(isWaitingHumanGate({ node_id: "a", status: "waiting_user" }, kinds), false);
  assert.equal(isWaitingHumanGate({ node_id: "unknown", status: "waiting_user" }, kinds), false);
  assert.equal(isWaitingHumanGate({ node_id: "g", status: "waiting_user" }, null), false);
});

// 审批动作走专用端点：普通留言端点永远不能批准（D4）。
test("the human-gate service posts the dedicated decision payload", () => {
  const svc = read("../src/services/task/humanGate.ts");
  assert.match(svc, /export async function postTaskHumanGate\(/);
  assert.match(svc, /\/api\/tasks\/\$\{encodeURIComponent\(taskId\)\}\/human-gate/);
  assert.match(svc, /root_id: rootId/);
  assert.match(svc, /node_id: input\.workflowNodeId/);
  assert.match(svc, /decision: input\.decision/);
  assert.match(svc, /appURL\([^;]*,\s*undefined,\s*nodeId\)/, "cross-node: the task node id must be passed explicitly");
  const svcCode = svc
    .split("\n")
    .filter((line) => !line.trim().startsWith("//"))
    .join("\n");
  assert.doesNotMatch(svcCode, /\/comments/, "a decision is never a normal comment");
});

// 运行视图仍是展示层：审批通过回调交给 logic 层，组件自己不取数、不发请求。
test("RunTimeline shows approve/reject only for a waiting HumanGate", () => {
  const component = read("../src/components/task/RunTimeline.tsx");
  assert.doesNotMatch(component, /\bfetch\s*\(/, "presentation layer must not fetch");
  assert.doesNotMatch(component, /services\//, "view layer must not import services");
  assert.match(component, /onDecideGate/, "the decision goes through a parent callback");

  const html = renderTimeline({
    run: { id: "run-gate" },
    nodeKinds: { g: "human_gate", a: "agent" },
    nodeRuns: [
      { id: "nr-gate", node_id: "g", status: "waiting_user", output: { prompt: "确认上线" } },
      { id: "nr-agent", node_id: "a", status: "waiting_user", output: { prompt: "not a gate" } },
      { id: "nr-done", node_id: "g", status: "success" },
    ],
    onDecideGate: () => {},
  });
  assert.match(html, /data-testid="human-gate-actions-nr-gate"/, "the waiting gate gets its own action row");
  assert.match(html, /data-testid="human-gate-approve-nr-gate"/, "approve must be addressable");
  assert.match(html, /data-testid="human-gate-reject-nr-gate"/, "reject must be addressable");
  assert.match(html, /确认上线/, "the gate prompt is shown by the output block");
  assert.doesNotMatch(html, /human-gate-actions-nr-agent/, "a waiting agent node is not a gate");
  assert.doesNotMatch(html, /human-gate-actions-nr-done/, "a decided gate shows no actions");

  // 没有回调（只读用法）时不得出现按钮。
  const readOnlyHtml = renderTimeline({
    run: { id: "run-gate-2" },
    nodeKinds: { g: "human_gate" },
    nodeRuns: [{ id: "nr-gate-2", node_id: "g", status: "waiting_user" }],
  });
  assert.doesNotMatch(readOnlyHtml, /human-gate-approve-nr-gate-2/, "read-only usage must not offer a decision");

  // 点击后、回包前必须禁用两个按钮，避免重复决定。
  const busyHtml = renderTimeline({
    run: { id: "run-gate-3" },
    nodeKinds: { g: "human_gate" },
    nodeRuns: [{ id: "nr-gate-3", node_id: "g", status: "waiting_user" }],
    onDecideGate: () => {},
    decidingNodeRunId: "nr-gate-3",
  });
  assert.match(busyHtml, /data-testid="human-gate-approve-nr-gate-3"[^>]*disabled/, "approve is disabled while deciding");
  assert.match(busyHtml, /data-testid="human-gate-reject-nr-gate-3"[^>]*disabled/, "reject is disabled while deciding");
});

test("the run-view hook decides once and refreshes the run afterwards", () => {
  const hook = read("../src/components/task/useRunViewEntry.ts");
  assert.match(hook, /postTaskHumanGate\(/, "the hook owns the write (views do not import services)");
  assert.match(hook, /decision/, "the decision is forwarded");
  assert.match(hook, /fetchWorkflowNodeRuns\(rootId, taskId, nodeId\)/, "the node runs are refreshed after a decision");
  assert.match(hook, /decidingNodeRunId/, "busy state is exposed to the view");
  assert.match(hook, /busyRef\.current|decidingId/, "duplicate clicks are gated");

  const entry = read("../src/components/task/RunViewEntry.tsx");
  assert.match(entry, /runNodeKinds\(/, "node kinds come from the run graph");
  assert.match(entry, /nodeKinds=\{/, "RunTimeline receives the node kinds");
  assert.match(entry, /onDecideGate=\{/, "RunTimeline receives the decision callback");
  assert.match(entry, /decidingNodeRunId=\{/, "RunTimeline receives the busy state");
});

test("human-gate labels are defined in both locales", () => {
  const zh = read("../src/i18n/locales/zh-CN-tasks.ts");
  const en = read("../src/i18n/locales/en-US-tasks.ts");
  for (const key of [
    "task.runTimeline.approve",
    "task.runTimeline.reject",
    "task.runTimeline.deciding",
    "task.runTimeline.gateFailed",
  ]) {
    assert.ok(zh.includes(`"${key}"`), `zh-CN must define ${key}`);
    assert.ok(en.includes(`"${key}"`), `en-US must define ${key}`);
  }
});