/**
 * 任务/看板助手的纯函数。
 * 拆自 appSupport.tsx（2026-09 App.tsx 拆分）。
 */

import {  MessageKey ,  MessageParams  } from "../i18n";
import {  KanbanTask ,  StageRun ,  StageTemplate ,  TaskDetail ,  TaskTemplate  } from "../services/tasks";

export function firstUserInputTemplate(template: TaskTemplate | null): string {
  const first = template?.stages?.[0]?.snapshot;
  return first?.role === "user" ? first.prompt_template || "" : "";
}

export function firstAgentStage(template: TaskTemplate | null): StageTemplate | null {
  return template?.stages?.map((stage) => stage.snapshot).find((stage) => stage.role === "agent") || null;
}

export function isUnfinishedKanbanTask(task: KanbanTask): boolean {
  return task.status !== "success" && task.status !== "fail" && task.status !== "cancelled";
}

export function isTerminalKanbanTask(task: KanbanTask): boolean {
  return task.status === "success" || task.status === "fail" || task.status === "cancelled";
}

export function parseTaskSessionErrorMessage(error?: string): string {
  const raw = String(error || "").trim();
  if (!raw) return "";
  try {
    const parsed = JSON.parse(raw) as { message?: unknown };
    return typeof parsed.message === "string" && parsed.message.trim() ? parsed.message.trim() : raw;
  } catch {
    return raw;
  }
}

export function parseTaskSessionErrorDetails(error?: string): string[] {
  const raw = String(error || "").trim();
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw) as { data?: unknown };
    if (Array.isArray(parsed.data)) return parsed.data.map((item) => String(item)).filter(Boolean);
    if (parsed.data === undefined || parsed.data === null) return [];
    return [String(parsed.data)];
  } catch {
    return [];
  }
}

export function taskStatusLabel(status: string, t: (key: MessageKey, params?: MessageParams) => string): string {
  const labels: Record<string, MessageKey> = {
    pending: "task.status.pending",
    queued: "task.status.queued",
    running: "task.status.running",
    waiting_user: "task.status.waitingUser",
    paused: "task.status.paused",
    success: "task.status.success",
    fail: "task.status.fail",
    cancelled: "task.status.cancelled",
    approved: "task.status.approved",
    rejected: "task.status.rejected",
  };
  return labels[status] ? t(labels[status]) : status || "-";
}

export function firstTaskInputFromDetail(detail: TaskDetail): string {
  return detail.stage_runs.find((run) => run.stage_index === 0)?.input || "";
}

export function latestTaskStageRun(detail: TaskDetail, stageIndex: number): StageRun | null {
  const runs = detail.stage_runs
    .filter((run) => run.stage_index === stageIndex)
    .sort((a, b) => {
      const aTime = Date.parse(a.created_at || a.updated_at || "");
      const bTime = Date.parse(b.created_at || b.updated_at || "");
      return (Number.isFinite(bTime) ? bTime : 0) - (Number.isFinite(aTime) ? aTime : 0);
    });
  return runs[0] || null;
}

export function taskSessionKeysFromDetail(detail: TaskDetail): string[] {
  const seen = new Set<string>();
  const keys: string[] = [];
  for (const run of detail.stage_runs) {
    const key = String(run.session_key || "").trim();
    if (!key || seen.has(key)) continue;
    seen.add(key);
    keys.push(key);
  }
  const mainKey = String(detail.task.main_session_key || "").trim();
  if (mainKey && !seen.has(mainKey)) {
    keys.push(mainKey);
  }
  return keys;
}

export function normalizeFastService(
  value: unknown,
): "" | "on" | "off" {
  return value === "on" || value === "off" ? value : "";
}

export type TaskInlineAttachment = {
  id: string;
  file: File;
  previewUrl?: string;
  isImage: boolean;
};

export type TaskInlineEditState = {
  templateId: string;
  templateName: string;
  text: string;
  /** 新建时的任务名 */
  name?: string;
  /** 覆盖下一个 agent 阶段的 agent（空 = 用模板里的） */
  agentOverride?: string;
  /** 覆盖下一个 agent 阶段的模型（空 = 用模板里的） */
  modelOverride?: string;
  /** 覆盖下一个 agent 阶段的 effort（空 = 用模板里的） */
  effortOverride?: string;
  previousInputs: Array<{ id: string; label: string; input: string }>;
  createWorktree: boolean;
  worktreeBranchMode: "new" | "existing";
  worktreeBranch: string;
  canToggleWorktree: boolean;
  attachments: TaskInlineAttachment[];
};

/**
 * 把 agent/model/effort 覆盖写到模板的**第一个** agent 段上。
 *
 * wire 上 stages 是拍平的 StageTemplate[]（不是模板里的 { snapshot } 包装），
 * 所以这里返回拍平后的段。模板里没有 agent 段、或什么都没覆盖时返回 undefined，
 * 让后端照模板走。
 */
export function applyStageOverride(
  template: TaskTemplate | null,
  override: { agent?: string; model?: string; effort?: string },
): StageTemplate[] | undefined {
  if (!template) return undefined;
  if (!override.agent && !override.model && !override.effort) return undefined;
  let touched = false;
  const stages = (template.stages || []).map((stage) => {
    const snapshot = stage.snapshot;
    if (snapshot?.role !== "agent") return snapshot;
    if (touched) return snapshot;
    touched = true;
    return {
      ...snapshot,
      ...(override.agent ? { agent: override.agent } : {}),
      ...(override.model ? { model: override.model } : {}),
      ...(override.effort ? { effort: override.effort } : {}),
    };
  });
  return touched ? stages : undefined;
}
