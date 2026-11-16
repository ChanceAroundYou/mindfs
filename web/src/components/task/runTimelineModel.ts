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