import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { test } from "vitest";
import React from "react";
import { renderToString } from "react-dom/server";
import { I18nProvider } from "../src/i18n/index.tsx";
import { RunNodeRunRow } from "../src/components/task/RunNodeRunRow.tsx";
import { runHistoryGroups, legacyRunsFromNodeRuns } from "../src/components/task/runTimelineModel.ts";

// 运行视图的多轮历史 + 逐节点会话入口。
//
// 任务 #87 第 1 条：多轮执行时运行视图只有一个块、一直在覆盖。根因是引擎只绑定
// 最新一条 WorkflowRun，视图也只画那一条。现在按 Run 分组逐轮画，轮次顺序由
// runHistoryGroups 这个纯函数决定（可单测）。
//
// 任务 #87 第 2 条：入口旁边那枚独立的工作流会话按钮彻底去掉，逐节点的「打开
// 会话」改成图标按钮 —— 用会话列表同款的会话类型图标（ModeIcon type="task"），
// 不是工作流图标；文字只留在 title / aria-label 里。

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

const nodeRun = (id, runId, nodeId, extra = {}) => ({
  id,
  run_id: runId,
  node_id: nodeId,
  attempt: 1,
  status: "success",
  ...extra,
});

const run = (id, nodes, nodeRuns) => ({
  id,
  task_id: "task-1",
  revision: 1,
  graph: { nodes, edges: [] },
  node_runs: nodeRuns,
});

test("按 Run 分组，轮的顺序 = 后端给的顺序（旧的在前）", () => {
  const groups = runHistoryGroups([
    run("run-1", [{ id: "n1", name: "研究", kind: "agent" }], [nodeRun("nr-1", "run-1", "n1")]),
    run("run-2", [{ id: "n2", name: "汇总", kind: "agent" }], [nodeRun("nr-2", "run-2", "n2")]),
  ]);
  assert.deepEqual(
    groups.map((g) => [g.round, g.run.id, g.nodeRuns.map((n) => n.id)]),
    [
      [1, "run-1", ["nr-1"]],
      [2, "run-2", ["nr-2"]],
    ],
    "rounds must be 1..N in the order the backend returned the runs",
  );
});

test("每轮用自己快照里的节点名，不串到别的轮", () => {
  const groups = runHistoryGroups([
    run("run-1", [{ id: "n1", name: "旧研究", kind: "agent" }], [nodeRun("nr-1", "run-1", "n1")]),
    run("run-2", [{ id: "n1", name: "新研究", kind: "agent" }], [nodeRun("nr-2", "run-2", "n1")]),
  ]);
  assert.equal(groups[0].nodeNames.n1, "旧研究");
  assert.equal(groups[1].nodeNames.n1, "新研究");
  assert.equal(groups[1].nodeKinds.n1, "agent");
});

test("没有节点执行行的轮次不占块，也不打乱轮次号", () => {
  const groups = runHistoryGroups([
    run("run-1", [{ id: "n1" }], []),
    run("run-2", [{ id: "n1" }], [nodeRun("nr-2", "run-2", "n1")]),
  ]);
  assert.equal(groups.length, 1, "an empty run must not leave a blank block");
  assert.equal(groups[0].round, 1, "visible rounds are numbered contiguously");
  assert.equal(groups[0].run.id, "run-2");
});

test("null / 缺字段的输入返回空数组，不抛错", () => {
  assert.deepEqual(runHistoryGroups(null), []);
  assert.deepEqual(runHistoryGroups(undefined), []);
  assert.deepEqual(runHistoryGroups([{ id: "" }]), []);
  assert.deepEqual(runHistoryGroups([{ id: "run-1" }]), []);
});

// 旧后端（只回平铺 node_runs、没有 runs）必须回落成「一轮」，否则升级窗口里
// 运行视图展开是空白。回落归一化在 hook 里做，这里钉住纯函数的语义。
test("旧后端回包归一化成一轮：Run 快照 + 平铺节点行", () => {
  const rows = [nodeRun("nr-1", "run-1", "n1"), nodeRun("nr-2", "run-1", "n2")];
  const snapshot = run("run-1", [{ id: "n1", name: "研究", kind: "agent" }], undefined);
  const legacy = legacyRunsFromNodeRuns(rows, snapshot, "run-1", "task-1");
  assert.equal(legacy.length, 1, "a flat payload is exactly one round");
  assert.equal(legacy[0].id, "run-1", "the engine snapshot keeps the real run id");
  const groups = runHistoryGroups(legacy);
  assert.equal(groups.length, 1);
  assert.equal(groups[0].nodeNames.n1, "研究", "node names come from the snapshot graph");
  assert.deepEqual(groups[0].nodeRuns.map((n) => n.id), ["nr-1", "nr-2"]);
});

test("旧后端连 Run 快照也取不到时仍然画出节点行", () => {
  const rows = [nodeRun("nr-1", "run-1", "n1")];
  const legacy = legacyRunsFromNodeRuns(rows, null, "run-1", "task-1");
  assert.equal(legacy.length, 1, "a missing snapshot must not hide the node rows");
  assert.equal(legacy[0].id, "run-1", "fall back to the bound run id");
  assert.deepEqual(runHistoryGroups(legacy)[0].nodeRuns.map((n) => n.id), ["nr-1"]);
});

test("没有节点行时回落不造出空轮次", () => {
  assert.deepEqual(legacyRunsFromNodeRuns([], null, "run-1", "task-1"), []);
  assert.deepEqual(legacyRunsFromNodeRuns(null, null, "run-1", "task-1"), []);
});

test("独立的「工作流会话」入口已彻底删除", () => {
  assert.equal(
    existsSync(new URL("../src/components/task/WorkflowSessionJump.tsx", import.meta.url)),
    false,
    "WorkflowSessionJump must be deleted, not hidden",
  );
  const entry = read("../src/components/task/RunViewEntry.tsx");
  assert.doesNotMatch(entry, /WorkflowSessionJump/, "RunViewEntry must not mount the jump button");
  assert.doesNotMatch(entry, /pickWorkflowSessionKey/, "the selection rule goes with the button");
  assert.doesNotMatch(read("../src/components/task/runTimelineModel.ts"), /pickWorkflowSessionKey/);
});

test("运行视图逐轮渲染，多轮时带轮次标签", () => {
  const entry = read("../src/components/task/RunViewEntry.tsx");
  assert.match(entry, /runHistoryGroups\(runs\)/, "the view must be driven by the pure grouping rule");
  assert.match(entry, /groups\.map\(/, "every round must get its own block");
  assert.match(entry, /run-round-\$\{group\.round\}/, "each block must be identifiable by round");
  assert.match(entry, /task\.runTimeline\.roundLabel/, "multi-round blocks must say which round they are");
});

const renderRow = (props) =>
  renderToString(
    React.createElement(I18nProvider, null, React.createElement(RunNodeRunRow, props)),
  );

const rowProps = {
  nodeRun: nodeRun("nr-1", "run-1", "n1", { agent: "claude", session_key: "sess-abc" }),
  displayName: "研究",
  isGate: false,
  deciding: false,
  onOpenSession: () => {},
};

test("节点行的会话入口是会话列表同款图标，不是工作流图标", () => {
  const src = read("../src/components/task/RunNodeRunRow.tsx");
  // 会话图标抽成了共享组件（2026-11-19）：会话列表与运行视图共用同一份几何，
  // 两处各写一份迟早会漂。断言的落点因此是「调用共享组件」而不是「自绘 ModeIcon」。
  assert.match(
    src,
    /import \{ SessionTypeIcon \} from "\.\.\/common\/SessionTypeIcon"/,
    "must reuse the shared session icon component",
  );
  assert.match(src, /<SessionTypeIcon type="task"/, "task sessions use the session-list task icon");
  assert.doesNotMatch(src, /WorkflowIcon/, "the workflow icon must not be used for a session jump");
  const icon = read("../src/components/common/SessionTypeIcon.tsx");
  assert.match(icon, /<ModeIcon type=\{type\}/, "the shared component is the session-list icon set");
  assert.match(icon, /<AgentIcon/, "the agent corner badge must stay");

  const html = renderRow(rowProps);
  assert.match(html, /data-testid="open-session-nr-1"/, "the jump must stay keyed by node run id");
  assert.match(html, /<button[^>]*>[\s\S]*<svg[\s\S]*<\/button>/, "the button body must be an icon");
  assert.doesNotMatch(html, />打开会话</, "the label must not be rendered as visible text");
  assert.doesNotMatch(html, />Open session</, "the label must not be rendered as visible text");
});

test("图标按钮仍对辅助技术说明用途", () => {
  const html = renderRow(rowProps);
  assert.match(html, /title="(打开会话|Open session)"/, "hover text must explain the icon");
  assert.match(html, /aria-label="(打开会话|Open session)"/, "assistive tech must get the same label");
});

test("没有会话键的节点不渲染入口", () => {
  const html = renderRow({
    ...rowProps,
    nodeRun: nodeRun("nr-2", "run-1", "n1", { agent: "claude" }),
  });
  assert.doesNotMatch(html, /open-session-nr-2/, "a node without a session must not offer a jump");
});

test("i18n：轮次标签在，删除的按钮文案不在", () => {
  const zh = read("../src/i18n/locales/zh-CN-tasks.ts");
  const en = read("../src/i18n/locales/en-US-tasks.ts");
  for (const key of ["task.runTimeline.roundLabel", "task.runTimeline.openSession"]) {
    assert.ok(zh.includes(`"${key}"`), `zh-CN must define ${key}`);
    assert.ok(en.includes(`"${key}"`), `en-US must define ${key}`);
  }
  for (const key of ["task.runTimeline.openWorkflowSession", "task.runTimeline.noWorkflowSession"]) {
    assert.ok(!zh.includes(`"${key}"`), `zh-CN must drop ${key}`);
    assert.ok(!en.includes(`"${key}"`), `en-US must drop ${key}`);
  }
});
