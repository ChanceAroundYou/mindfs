// 运行视图的纯派生规则（无 React、无副作用）。
//
// NodeRun 本身不带节点类型，HumanGate 的审批入口必须回查 run 快照里的
// graph.nodes[].kind —— 只看 status="waiting_user" 会把条件求值失败、Agent
// 追问等同样停住的节点误当成可批准的网关。

import type { MessageKey, MessageParams } from "../../i18n";
import type { NodeRunPayload, WorkflowRunNodeRuns } from "../../shared/taskComments";
import { taskStatusLabel } from "../../app/appTask";

export type RunGraphLike = { nodes?: Array<{ id?: string; name?: string; kind?: string }> } | null | undefined;

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

// 节点 id → 显示名（来自这一轮 Run 的 graph 快照）。缺名字时回落到 node_id，
// 与 RunTimeline 的兜底一致。
export function runNodeNames(graph: RunGraphLike): Record<string, string> {
  const names: Record<string, string> = {};
  for (const node of graph?.nodes ?? []) {
    if (!node?.id) continue;
    names[node.id] = node.name || node.id;
  }
  return names;
}

// 运行视图的一轮：Run 快照 + 这一轮自己的节点执行行 + 从快照派生的展示表。
//
// 任务每发一条留言就可能新建一条 WorkflowRun，而 task_execution_engines 只绑定
// 最新一条非终态 Run。视图只画「当前绑定」会让多轮任务每轮把上一轮整块覆盖掉 ——
// 所以按 Run 分组、逐轮展示，round 是展示顺序（1 起，旧的在前）。
export type RunHistoryGroup = {
  run: WorkflowRunNodeRuns;
  nodeRuns: NodeRunPayload[];
  nodeNames: Record<string, string>;
  nodeKinds: Record<string, string>;
  round: number;
};

// 没有节点执行行的轮次不占位：Run 刚建好、节点还没物化时它什么也画不出来，
// 留一个空块只会让人以为执行丢了。空轮次跳过，轮次号在可见轮次间连续编号。
export function runHistoryGroups(
  runs: ReadonlyArray<WorkflowRunNodeRuns> | null | undefined,
): RunHistoryGroup[] {
  if (!Array.isArray(runs)) return [];
  const groups: RunHistoryGroup[] = [];
  for (const run of runs) {
    if (!run?.id) continue;
    const nodeRuns = Array.isArray(run.node_runs) ? run.node_runs : [];
    if (nodeRuns.length === 0) continue;
    groups.push({
      run,
      nodeRuns,
      nodeNames: runNodeNames(run.graph),
      nodeKinds: runNodeKinds(run.graph),
      round: groups.length + 1,
    });
  }
  return groups;
}

// 旧后端（`/workflow-run/nodes` 只回平铺 `node_runs`、没有 `runs`）的归一化：
// 把平铺节点行包成「一轮」。不归一化的话展示层拿到 `runs=null` 会「加载结束、
// 什么都不画」—— 前后端版本不一致的窗口里，运行视图展开就是一片空白。
//
// Run 快照尽力而为：取不到时用一个最小 Run 顶替（节点名回落成 node_id，没有
// graph 就没有节点类型），把已经发生的执行记录藏起来比缺节点名更糟。
export function legacyRunsFromNodeRuns(
  nodeRuns: ReadonlyArray<NodeRunPayload> | null | undefined,
  snapshot: WorkflowRunNodeRuns | null | undefined,
  runId: string,
  taskId: string,
): WorkflowRunNodeRuns[] {
  const rows = Array.isArray(nodeRuns) ? nodeRuns : [];
  if (rows.length === 0) return [];
  return [{ ...(snapshot ?? { id: runId, task_id: taskId, revision: 0 }), node_runs: rows }];
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