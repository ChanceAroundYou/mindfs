/**
 * 详情合并时保住任务的跨节点归属。
 *
 * 服务端不返 `_nodeId`：只有「取数的那一刻」的调用方知道数据来自哪台机器。
 * 工作台打开任务时会按来源节点打标，但面板自己的按编号重拉（`useTaskDetailPanel`
 * 的 refresh / mount effect）与 WS 局部更新拿到的都是一份**裸** detail；它们直接
 * 覆盖在册记录后，任务就失去归属，后续请求回落到 active node —— 跨节点任务在那
 * 边没有这个 root，用户看到「对话加载失败: root not found」，点重试还是同一个
 * 未知节点的请求。
 *
 * 规则：数据自带 `_nodeId` 时以它为准（数据最权威）；缺失时沿用同一 taskId
 * 已在册的那份。任务 id 全局唯一，不存在「同一任务在两台机器上各有一份」，
 * 所以沿用是安全的。
 */
export type TaskNodeTaggedDetail = {
  task?: { id?: unknown; _nodeId?: unknown } | null;
};

export function carryTaskNodeTag<D extends TaskNodeTaggedDetail>(
  detail: D,
  previous?: TaskNodeTaggedDetail | null,
): D {
  const task = detail?.task;
  if (!task) return detail;
  if (String(task._nodeId || "").trim()) return detail;
  const carried = String(previous?.task?._nodeId || "").trim();
  if (!carried) return detail;
  return { ...detail, task: { ...task, _nodeId: carried } };
}
