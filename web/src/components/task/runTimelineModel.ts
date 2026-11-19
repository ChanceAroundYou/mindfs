// 运行视图的纯派生规则（无 React、无副作用）。
//
// NodeRun 本身不带节点类型，HumanGate 的审批入口必须回查 run 快照里的
// graph.nodes[].kind —— 只看 status="waiting_user" 会把条件求值失败、Agent
// 追问等同样停住的节点误当成可批准的网关。

import type { MessageKey, MessageParams } from "../../i18n";
import { taskStatusLabel } from "../../app/appTask";

export type RunGraphLike = { nodes?: Array<{ id?: string; kind?: string }> } | null | undefined;

// 节点状态 → 颜色令牌。运行视图与历史运行共用这一份，
// 否则同一个「成功/失败」在两条时间线上会是两种绿/红。
// 色值走 index.css 的 --status-* token，与看板卡/任务详情（appTask.taskStatusColor）同源。
// approved / rejected 是历史运行里网关的审批结果，不归一成 success。
export function nodeRunStatusColor(status: string): string {
  switch (status) {
    case "success":
    case "approved":
      return "var(--status-ok)";
    case "running":
      return "var(--accent-color)";
    case "fail":
    case "cancelled":
    case "rejected":
      return "var(--status-bad)";
    case "waiting_user":
      return "var(--status-warn)";
    case "skipped":
    case "unknown":
    case "pending":
    case "queued":
    default:
      return "var(--text-secondary)";
  }
}

// NodeRun 专属状态 → 文案。queued / skipped / unknown 只存在于节点执行记录，
// 不属于任务状态词表（taskStatusLabel 刻意不收，守卫测试钉住不让它们复活成
// 任务状态）；其余状态复用任务词表，避免两处各写一套「成功/失败」。
const NODE_ONLY_STATUS_LABELS: Record<string, MessageKey> = {
  queued: "task.runTimeline.status.queued",
  skipped: "task.runTimeline.status.skipped",
  unknown: "task.runTimeline.status.unknown",
};

export function nodeRunStatusLabel(
  status: string,
  t: (key: MessageKey, params?: MessageParams) => string,
): string {
  const key = NODE_ONLY_STATUS_LABELS[status];
  return key ? t(key) : taskStatusLabel(status, t);
}

export function runNodeKinds(graph: RunGraphLike): Record<string, string> {
  const kinds: Record<string, string> = {};
  for (const node of graph?.nodes ?? []) {
    if (!node?.id || !node.kind) continue;
    kinds[node.id] = node.kind;
  }
  return kinds;
}

export function isWaitingHumanGate(
  nodeRun: { node_id: string; status: string },
  nodeKinds: Record<string, string> | null | undefined,
): boolean {
  return nodeRun.status === "waiting_user" && nodeKinds?.[nodeRun.node_id] === "human_gate";
}

// 「跳到工作流会话」的落点：运行中 > 等待用户 > 最近一条有会话的节点。
//
// 用户点这个按钮的目的通常是「看看它现在在干什么 / 接管它」，所以活着的节点
// 优先；全部收尾后落回最近一次执行（ListWorkflowNodeRuns 按 created_at 升序）。
// 没有 session_key 的节点跳过 —— 跳过去只会打开空页面。
export function pickWorkflowSessionKey(
  nodeRuns: ReadonlyArray<{ status?: string; session_key?: string }> | null | undefined,
): string {
  const usable = (nodeRuns || []).filter((nodeRun) => (nodeRun.session_key || "").trim() !== "");
  if (usable.length === 0) return "";
  const byStatus = (status: string) => usable.find((nodeRun) => nodeRun.status === status);
  const live = byStatus("running") || byStatus("waiting_user");
  if (live) return (live.session_key as string).trim();
  return (usable[usable.length - 1].session_key as string).trim();
}

// 节点耗时（started_at → finished_at）。缺任一端、时间非法或倒挂都返回 null：
// 运行视图宁可不显示，也不能编一个数出来。
export function nodeRunDurationMs(nodeRun: {
  started_at?: string;
  finished_at?: string;
}): number | null {
  if (!nodeRun.started_at || !nodeRun.finished_at) return null;
  const start = Date.parse(nodeRun.started_at);
  const end = Date.parse(nodeRun.finished_at);
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) return null;
  return end - start;
}

// 耗时的展示文案：一分钟以内按秒，超过按「分 秒」。文案走 i18n，不在这里硬编码。
export function formatNodeRunDuration(
  ms: number,
  t: (key: MessageKey, params?: MessageParams) => string,
): string {
  const seconds = Math.max(1, Math.round(ms / 1000));
  if (seconds < 60) return t("task.runTimeline.durationSeconds", { n: seconds });
  return t("task.runTimeline.durationMinutes", {
    m: Math.floor(seconds / 60),
    s: seconds % 60,
  });
}