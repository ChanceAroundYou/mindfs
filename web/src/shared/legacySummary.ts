import type { StageRun } from "./taskTypes";

// 历史任务（阶段模式）的总结生成。
//
// 为什么要有这一层：对话式任务的总结来自 activity.summary，而模板时代的任务
// 没有 activities。任务详情面板要显示「Agent 最后说了什么」，只能从 stage_runs
// 反推；推不出时降级成阶段计数，绝不渲染空白。
//
// stage_runs 线上不落 agent 输出（只有 input / rendered_prompt），所以 `output`
// 是前向兼容字段：后端哪天补上就会自动启用，没有时走计数降级。

export type LegacySummaryStageRun = StageRun & { output?: string };

export const EMPTY_LEGACY_SUMMARY = "任务已完成（无详细记录）";

const MAX_SUMMARY_CHARS = 500;

function isCompleted(status: string): boolean {
  // legacy 引擎的完成态是 "success"；"completed" 兼容 activity 语义与测试数据。
  return status === "success" || status === "completed";
}

export function extractSummaryFromStageRuns(
  stageRuns: LegacySummaryStageRun[] | null | undefined,
  emptyText: string = EMPTY_LEGACY_SUMMARY,
): string {
  const runs = stageRuns ?? [];
  if (runs.length === 0) return emptyText;

  const lastAgentRun = runs
    .filter((run) => run.role === "agent" && isCompleted(run.status))
    .pop();

  const output = (lastAgentRun?.output ?? "").trim();
  if (output) {
    return output.length <= MAX_SUMMARY_CHARS
      ? output
      : `${output.slice(0, MAX_SUMMARY_CHARS)}...`;
  }

  const completed = runs.filter((run) => isCompleted(run.status)).length;
  return `已完成 ${completed}/${runs.length} 个阶段`;
}