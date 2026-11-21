import type { MessageKey } from "../../i18n/index";
import type { RouterDecisionSummary } from "../../shared/routerDecisions";

// 监管记录（S4）的纯函数：哪些决策算「Router 自己动过手」、怎么排序、怎么读。
//
// 后端 router-decisions 已经按时间倒序下发，这里再排一次是为了让视图不依赖
// 端点顺序（同一时刻的决策保持后端顺序，不因两次排序抖动）。

const SUPERVISION_TRIGGERS = new Set(["run_failed", "run_stalled", "direct_stalled"]);
const SUPERVISION_ACTIONS = new Set([
  "retry_node",
  "retry_direct",
  "resume_run",
  "notify_user",
  "escalate",
  "cancel_node",
  "cancel_run",
  "cancel_task",
]);

const TRIGGER_KEYS: Record<string, MessageKey> = {
  run_failed: "task.supervision.trigger.run_failed",
  run_stalled: "task.supervision.trigger.run_stalled",
  direct_stalled: "task.supervision.trigger.direct_stalled",
};

const ACTION_KEYS: Record<string, MessageKey> = {
  retry_node: "task.supervision.action.retry_node",
  retry_direct: "task.supervision.action.retry_direct",
  resume_run: "task.supervision.action.resume_run",
  notify_user: "task.supervision.action.notify_user",
  escalate: "task.supervision.action.escalate",
  cancel_node: "task.supervision.action.cancel_node",
  cancel_run: "task.supervision.action.cancel_run",
  cancel_task: "task.supervision.action.cancel_task",
};

const STATUS_KEYS: Record<string, MessageKey> = {
  applied: "task.supervision.status.applied",
  applying: "task.supervision.status.applying",
  rejected: "task.supervision.status.rejected",
  failed: "task.supervision.status.failed",
  skipped: "task.supervision.status.skipped",
  shadow: "task.supervision.status.shadow",
};

export type SupervisionRow = {
  id: string;
  trigger: string;
  /** 只含监管动作（普通 say / silent 之类的同轮动作不进这一列）。 */
  actions: string[];
  status: string;
  error: string;
  createdAt: string;
};

export function supervisionTriggerKey(kind: string): MessageKey | null {
  return TRIGGER_KEYS[kind] || null;
}

export function supervisionActionKey(kind: string): MessageKey | null {
  return ACTION_KEYS[kind] || null;
}

export function supervisionStatusKey(status: string): MessageKey | null {
  return STATUS_KEYS[status] || null;
}

export function isSupervisionDecision(decision: RouterDecisionSummary | null | undefined): boolean {
  if (!decision) return false;
  if (SUPERVISION_TRIGGERS.has(decision.trigger_kind)) return true;
  return (decision.action_kinds || []).some((kind) => SUPERVISION_ACTIONS.has(kind));
}

function supervisionActions(kinds: string[] | undefined): string[] {
  const seen = new Set<string>();
  const actions: string[] = [];
  for (const kind of kinds || []) {
    if (!SUPERVISION_ACTIONS.has(kind) || seen.has(kind)) continue;
    seen.add(kind);
    actions.push(kind);
  }
  return actions;
}

function createdAtValue(iso: string): number {
  const parsed = Date.parse(iso || "");
  return Number.isFinite(parsed) ? parsed : Number.NEGATIVE_INFINITY;
}

/** 过滤 + 排序 + 归约成展示行；空输入返回空数组。 */
export function supervisionRows(decisions: RouterDecisionSummary[] | null | undefined): SupervisionRow[] {
  const rows = (decisions || []).filter(isSupervisionDecision).map((decision) => ({
    id: decision.id,
    trigger: decision.trigger_kind,
    actions: supervisionActions(decision.action_kinds),
    status: decision.status,
    error: (decision.error || "").trim(),
    createdAt: decision.created_at,
  }));
  // Array.prototype.sort 在现代引擎里稳定；显式带下标兜底，保证同一时刻不乱序。
  return rows
    .map((row, index) => ({ row, index }))
    .sort((a, b) => createdAtValue(b.row.createdAt) - createdAtValue(a.row.createdAt) || a.index - b.index)
    .map((entry) => entry.row);
}

/** 行首的本地时刻（HH:MM）；坏时间回空串，让视图自己决定怎么显示。 */
export function supervisionRowClock(iso: string): string {
  const parsed = Date.parse(iso || "");
  if (!Number.isFinite(parsed)) return "";
  const date = new Date(parsed);
  return `${String(date.getHours()).padStart(2, "0")}:${String(date.getMinutes()).padStart(2, "0")}`;
}
