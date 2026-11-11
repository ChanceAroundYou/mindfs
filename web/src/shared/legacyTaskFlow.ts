import type { StageRun, StageTemplate } from "./taskTypes";

// 模板时代任务的流水 → 工作流形状的只读投影（Phase 2）。
//
// 为什么不是 MigrateTaskToWorkflowRun：那是**执行级**迁移，且明确拒绝终态任务；
// 历史任务绝大多数已经结束，用它既迁不动，也会给终态任务造出无人认领的 Run。
// 这里只做展示投影 —— 不取数、不写库、不改输入，任务仍由 legacy stages 引擎推进。
//
// 状态语义照搬 TaskDetailPanel 的既有规则：同一 stage_index 有多条 run 时取
// `updated_at` 最新的一条；没有 run 的段就是 pending。legacy 的 user 段会产生
// `approved` / `rejected`（审批结果），这两种状态原样保留，不要归一成 success。

export type LegacyFlowNodeKind = "agent" | "human_gate";

export type LegacyFlowNode = {
  /** 稳定节点 id：优先用 stage 自带 id，否则用 1-based 序号兜底。 */
  id: string;
  /** 1-based 段序号，与后端 stage_index 一致。 */
  index: number;
  name: string;
  kind: LegacyFlowNodeKind;
  /** 原样透传 stage_run.status；没有 run 时为 "pending"。 */
  status: string;
  /** 已执行时用 run.rendered_prompt，否则用片段模板文本。 */
  prompt: string;
  agent?: string;
  model?: string;
  sessionKey?: string;
  startedAt?: string;
  finishedAt?: string;
};

export type LegacyTaskFlowInput = {
  stages?: StageTemplate[] | null;
  stageRuns?: StageRun[] | null;
  currentStageIndex: number;
  taskStatus: string;
};

export type LegacyTaskFlow = {
  nodes: LegacyFlowNode[];
};

function latestRunByStage(stageRuns: StageRun[]): Map<number, StageRun> {
  const latest = new Map<number, StageRun>();
  for (const run of stageRuns) {
    const existing = latest.get(run.stage_index);
    if (!existing || run.updated_at > existing.updated_at) latest.set(run.stage_index, run);
  }
  return latest;
}

export function legacyTaskFlow(input: LegacyTaskFlowInput): LegacyTaskFlow {
  const stages = input.stages ?? [];
  const latest = latestRunByStage(input.stageRuns ?? []);
  const nodes = stages.map((stage, position): LegacyFlowNode => {
    const index = position + 1;
    const run = latest.get(index);
    return {
      id: stage.id || `legacy-stage-${index}`,
      index,
      name: stage.name || "",
      kind: stage.role === "user" ? "human_gate" : "agent",
      status: run?.status || "pending",
      prompt: run?.rendered_prompt || stage.prompt_template || "",
      agent: stage.agent,
      model: stage.model,
      sessionKey: run?.session_key,
      startedAt: run?.started_at,
      finishedAt: run?.finished_at,
    };
  });
  return { nodes };
}
