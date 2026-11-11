import assert from "node:assert/strict";
import { test } from "vitest";
import { legacyTaskFlow } from "../src/shared/legacyTaskFlow.ts";
import type { StageRun, StageTemplate } from "../src/shared/taskTypes.ts";

// Phase 2：模板时代任务的流水在详情里按工作流语法呈现。
//
// 投影必须是纯函数：只读 stages + stage_runs，不取数、不写库、不改输入。
// 历史任务绝大多数是终态，既有的 MigrateTaskToWorkflowRun 拒绝终态任务，
// 所以这里做的是**只读展示投影**，不是执行引擎迁移。

function stage(role: StageTemplate["role"], name: string, prompt: string): StageTemplate {
  return { name, role, prompt_template: prompt };
}

function run(stageIndex: number, status: string, updatedAt: string, extra: Partial<StageRun> = {}): StageRun {
  return {
    id: `run-${stageIndex}-${updatedAt}`,
    task_id: "task-1",
    stage_index: stageIndex,
    stage_name: `stage-${stageIndex}`,
    role: "agent",
    status,
    created_at: updatedAt,
    updated_at: updatedAt,
    ...extra,
  };
}

test("把线性 stages 投影成按顺序排列的节点", () => {
  const flow = legacyTaskFlow({
    stages: [stage("user", "任务输入", "做点什么"), stage("agent", "执行", "{previous_input}")],
    stageRuns: [],
    currentStageIndex: 0,
    taskStatus: "pending",
  });
  assert.deepEqual(flow.nodes.map((node) => node.index), [1, 2]);
  assert.deepEqual(flow.nodes.map((node) => node.name), ["任务输入", "执行"]);
});

test("user 段映射为 human_gate，agent 段映射为 agent", () => {
  const flow = legacyTaskFlow({
    stages: [stage("user", "输入", "a"), stage("agent", "执行", "b")],
    stageRuns: [],
    currentStageIndex: 0,
    taskStatus: "pending",
  });
  assert.equal(flow.nodes[0].kind, "human_gate");
  assert.equal(flow.nodes[1].kind, "agent");
});

test("同一段有多次 run 时取 updated_at 最新的一条", () => {
  const flow = legacyTaskFlow({
    stages: [stage("agent", "执行", "prompt")],
    stageRuns: [
      run(1, "fail", "2026-11-01T00:00:00Z"),
      run(1, "success", "2026-11-02T00:00:00Z", { rendered_prompt: "执行后的提示词" }),
      run(1, "running", "2026-11-01T12:00:00Z"),
    ],
    currentStageIndex: 1,
    taskStatus: "success",
  });
  assert.equal(flow.nodes[0].status, "success");
  assert.equal(flow.nodes[0].prompt, "执行后的提示词");
});

test("没有 run 的段是 pending，未执行时显示模板文本", () => {
  const flow = legacyTaskFlow({
    stages: [stage("agent", "执行", "原始模板")],
    stageRuns: [],
    currentStageIndex: 0,
    taskStatus: "pending",
  });
  assert.equal(flow.nodes[0].status, "pending");
  assert.equal(flow.nodes[0].prompt, "原始模板");
});

test("legacy 的真实状态 approved / rejected 原样保留", () => {
  const flow = legacyTaskFlow({
    stages: [stage("user", "输入", "a"), stage("user", "复核", "b")],
    stageRuns: [run(1, "approved", "2026-11-01T00:00:00Z"), run(2, "rejected", "2026-11-01T00:00:00Z")],
    currentStageIndex: 2,
    taskStatus: "cancelled",
  });
  assert.deepEqual(flow.nodes.map((node) => node.status), ["approved", "rejected"]);
});

test("带上 agent / model / session / 时间戳", () => {
  const flow = legacyTaskFlow({
    stages: [{ name: "执行", role: "agent", prompt_template: "p", agent: "claude", model: "sonnet" }],
    stageRuns: [run(1, "success", "2026-11-02T00:00:00Z", {
      session_key: "sess-1",
      started_at: "2026-11-02T00:00:01Z",
      finished_at: "2026-11-02T00:00:09Z",
    })],
    currentStageIndex: 1,
    taskStatus: "success",
  });
  assert.equal(flow.nodes[0].agent, "claude");
  assert.equal(flow.nodes[0].model, "sonnet");
  assert.equal(flow.nodes[0].sessionKey, "sess-1");
  assert.equal(flow.nodes[0].startedAt, "2026-11-02T00:00:01Z");
  assert.equal(flow.nodes[0].finishedAt, "2026-11-02T00:00:09Z");
});

test("空 stages 得到空图，且不修改输入", () => {
  const stages = [stage("agent", "执行", "p")];
  const stageRuns = [run(1, "success", "2026-11-01T00:00:00Z")];
  const stagesBefore = JSON.stringify(stages);
  const runsBefore = JSON.stringify(stageRuns);
  const flow = legacyTaskFlow({ stages: [], stageRuns: [], currentStageIndex: 0, taskStatus: "pending" });
  assert.deepEqual(flow.nodes, []);
  legacyTaskFlow({ stages, stageRuns, currentStageIndex: 1, taskStatus: "success" });
  assert.equal(JSON.stringify(stages), stagesBefore);
  assert.equal(JSON.stringify(stageRuns), runsBefore);
});
