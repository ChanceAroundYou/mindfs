/**
 * 任务/看板助手的纯函数。
 * 拆自 appSupport.tsx（2026-09 App.tsx 拆分）。
 */

import {  MessageKey ,  MessageParams  } from "../i18n";
import {  KanbanTask ,  StageRun ,  StageTemplate ,  TaskDetail ,  TaskTemplate  } from "../services/tasks";

/** 新建 agent 段时的默认 agent/模型：claude + sonnet，别再默认 codex。 */
export const DEFAULT_TASK_AGENT = "claude";
export const DEFAULT_TASK_MODEL = "sonnet";

export function firstUserInputTemplate(template: TaskTemplate | null): string {
  const first = template?.stages?.[0]?.snapshot;
  return first?.role === "user" ? first.prompt_template || "" : "";
}

export function firstAgentStage(template: TaskTemplate | null): StageTemplate | null {
  return template?.stages?.map((stage) => stage.snapshot).find((stage) => stage.role === "agent") || null;
}

/**
 * 新增阶段时继承的那一段：紧挨着新段的上一段 agent 段。
 * 没有 agent 段就退回默认（claude + sonnet）。
 */
export function inheritAgentStage(stages: StageTemplate[], fromIndex: number): StageTemplate {
  const previous = stages
    .slice(0, fromIndex)
    .reverse()
    .find((stage) => stage.role === "agent");
  return {
    name: "",
    role: "agent",
    agent: previous?.agent || DEFAULT_TASK_AGENT,
    model: previous?.model || DEFAULT_TASK_MODEL,
    mode: previous?.mode || "",
    effort: previous?.effort || "",
    fast_service: previous?.fast_service || "",
    plan_mode: previous?.plan_mode === true,
    session_reuse_policy: previous?.session_reuse_policy || "task_main",
    prompt_template: "",
    auto_advance: false,
  };
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

/**
 * 任务状态色。看板卡、工作台卡、任务详情面板共用这一份 —— 三处各写一套的话，
 * 「已完成到底该是绿还是灰」迟早会分叉。
 *
 * 色值走 index.css 的 --status-* token，深浅主题各自给值（meadow/moss 是浅色，继承 :root）。
 * 注意别和 stream/ToolCallCard.tsx 里那份 statusColors 混：那份是工具调用的生命周期
 * （in_progress / complete / error），词表不同，混用会让两边互相污染。
 */
export function taskStatusColor(status: string): string {
  const colors: Record<string, string> = {
    pending: "var(--text-secondary)",
    queued: "var(--accent-color)",
    running: "var(--accent-color)",
    waiting_user: "var(--status-warn)",
    paused: "var(--text-secondary)",
    success: "var(--status-ok)",
    approved: "var(--status-ok)",
    fail: "var(--status-bad)",
    rejected: "var(--status-bad)",
    cancelled: "var(--text-secondary)",
  };
  return colors[status] || "var(--text-secondary)";
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

export function currentTaskInputFromDetail(detail: TaskDetail): string {
  return latestTaskStageRun(detail, detail.task.current_stage_index)?.input || "";
}

export function previousTaskInputsFromDetail(detail: TaskDetail, t: (key: MessageKey, params?: MessageParams) => string): Array<{ id: string; label: string; input: string }> {
  const items: Array<{ id: string; label: string; input: string }> = [];
  for (let index = 0; index < detail.task.current_stage_index; index += 1) {
    const run = latestTaskStageRun(detail, index);
    const input = run?.input || "";
    if (!run || !input.trim()) continue;
    items.push({
      id: run.id,
      label: run.stage_name || t("task.stageLabel", { index: index + 1 }),
      input,
    });
  }
  return items;
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
  /**
   * 面板的目标项目。从工作台发起时是那个面板里选的项目，不一定是当前项目；
   * 缺省表示当前项目（看板入口永远落在当前项目上）。
   */
  targetRootId?: string;
  /**
   * 面板上是否显示「项目」下拉。只有工作台入口会置 true（它不知道当前在哪个项目）；
   * 看板入口为 false —— 那里项目已经定了，多一个选不了的项目下拉只是噪声。
   * 换项目时要重算 worktree 偏好和 canToggleWorktree，存的是「每个项目各一份」的值。
   */
  allowProjectSwitch?: boolean;
  createWorktreePerRoot?: Record<string, { createWorktree: boolean; worktreeBranchMode: "new" | "existing"; worktreeBranch: string }>;
  /** 新建时的任务名 */
  name?: string;
  /** 覆盖下一个 agent 阶段的 agent（空 = 用模板里的） */
  agentOverride?: string;
  /** 覆盖下一个 agent 阶段的模型（空 = 用模板里的） */
  modelOverride?: string;
  /** 覆盖下一个 agent 阶段的 effort（空 = 用模板里的） */
  effortOverride?: string;
  /**
   * 覆盖首段的「立即执行」（缺省 = 用模板里的）。
   * 开 → 创建后直接推进到下一个 agent 段跑起来；关 → 停在「未开始」。
   */
  startImmediately?: boolean;
  previousInputs: Array<{ id: string; label: string; input: string }>;
  createWorktree: boolean;
  worktreeBranchMode: "new" | "existing";
  worktreeBranch: string;
  canToggleWorktree: boolean;
  attachments: TaskInlineAttachment[];
};

/**
 * 把 agent/model/effort 覆盖写到模板的**第一个** agent 段上，
 * 把 startImmediately 覆盖写到首段（任务输入）的「立即执行」上。
 *
 * wire 上 stages 是拍平的 StageTemplate[]（不是模板里的 { snapshot } 包装），
 * 所以这里返回拍平后的段。模板里没有 agent 段、什么都没覆盖时返回 undefined，
 * 让后端照模板走。
 *
 * startImmediately 是**每次都照实发**的（面板开出来时就填了模板的值，不传才是
 * 「别管」）：只在 true 时写会让「面板上把模板勾上的那颗取消掉」退化成「照模板走」，
 * 任务照样自己跑起来 —— 面板上看得见的选择必须压得住后端。
 */
export function applyStageOverride(
  template: TaskTemplate | null,
  override: { agent?: string; model?: string; effort?: string; startImmediately?: boolean },
): StageTemplate[] | undefined {
  if (!template) return undefined;
  if (!override.agent && !override.model && !override.effort && override.startImmediately === undefined) return undefined;
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
  if (override.startImmediately !== undefined && stages[0]) {
    // 首段必是 user 段（后端 CreateTask 也这么校验），「立即执行」挂在这儿。
    // 模板里没有 agent 段时下面会返回 undefined，反正也没有下一段可推进。
    stages[0] = { ...stages[0], start_immediately: override.startImmediately };
  }
  return touched ? stages : undefined;
}
