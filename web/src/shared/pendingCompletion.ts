import { sessionKeyNodeId } from "./scope";

/**
 * 跨节点「刚完成」差分：从「回复中」真相表的两轮快照里找出**这一轮刚结束**的会话。
 *
 * 为什么需要它：浏览器只连当前激活节点的一条 WS，远端节点（PC/fn）的 `session.done`
 * 根本不会推过来 —— 那些会话的完成音与未读徽标在原实现里从来没有触发点。唯一跨节点的
 * 活通道是 `useReplyingSessionPoll` 的 5s 轮询，它维护的 pending 真相表 true→false
 * （或整条消失）就是「这条刚跑完」。这里刻意不新增通道/不碰后端：轮询已经在跑，
 * 差分只是读它前后两帧。
 *
 * 三条排除，缺一条就会误报：
 *  - **节点必须本轮真的问到**：`mergeReplyingStateByNode` 对失败的节点保留旧值，
 *    那些键在 next 里仍是 true，本来就不会被判为完成；这里再显式拦一次，
 *    是为了以后有人改合并逻辑时差分不会跟着错。
 *  - **激活节点不算**：那条节点的 `session.done` 由 WS 实时处理过（已经响过、已经标过），
 *    轮询里再判一次会连响两遍。
 *  - **归属不明的历史键不算**：空 nodeId 的键反推不出节点，标未读会落到错误的键上。
 */
export function newlyCompletedPendingKeys(
  previous: Record<string, boolean>,
  next: Record<string, boolean>,
  okNodeIds: Iterable<string>,
  excludeNodeId = "",
): string[] {
  const ok = new Set(
    Array.from(okNodeIds, (v) => String(v || "").trim()).filter(Boolean),
  );
  const excluded = String(excludeNodeId || "").trim();
  const completed: string[] = [];
  for (const [key, wasPending] of Object.entries(previous || {})) {
    if (!wasPending) continue;
    if (next[key]) continue; // 还在跑
    const nid = sessionKeyNodeId(key);
    if (!nid) continue; // 历史单节点键：归属不明
    if (!ok.has(nid)) continue; // 本轮没问到那个节点，完成与否无从判断
    if (excluded && nid === excluded) continue; // 激活节点已由 WS 处理
    completed.push(key);
  }
  return completed;
}

/**
 * 从「刚完成」里挑出**该响提示音**的那些：子会话（Claude Code Task 子代理 / 看板节点
 * attach 的会话）跑完不响。
 *
 * 用户要的是「活干完了」，不是「其中一个子任务回来了」——父会话那一轮往往还在跑，
 * 子代理回来的那一下弹提示音纯属噪音。WS 路径早就这么做（`realtimeWsHandlers` 判
 * `parent_session_key`），轮询路径补上同一语义。
 *
 * 只影响提示音：未读徽标照样逐条标，那是静态信息，不吵人。
 */
export function alertableCompletedKeys(
  completed: Iterable<string>,
  subSessionKeys: Iterable<string>,
): string[] {
  const subSessions = new Set(
    Array.from(subSessionKeys, (v) => String(v || "").trim()).filter(Boolean),
  );
  const alertable: string[] = [];
  for (const key of completed) {
    if (!subSessions.has(key)) alertable.push(key);
  }
  return alertable;
}

/**
 * 维护「哪些键是子会话」这张表。
 *
 * 必须是跨帧的表，不能只看当前快照：完成是靠「从 pending 表里消失」差分出来的，
 * 判完成的那一帧里会话已经不在 `sessions` 里了，它的 `subSession` 字段也就读不到。
 * 所以：仍在 replying 的键保留标记，消失即忘（会话结束后不会再复用同一个键）；
 * 本轮快照新标出的键加进来。
 *
 * `stillPending` 要传**合并后**的 pending 表：节点请求失败的键在那里仍是 true，
 * 标记不能因为这一轮没拉到快照就被丢掉。
 */
export function carrySubSessionKeys(
  previous: Iterable<string>,
  fresh: Iterable<string>,
  stillPending: Record<string, boolean>,
): Set<string> {
  const next = new Set<string>();
  for (const key of previous) {
    if (stillPending[key]) next.add(key);
  }
  for (const key of fresh) {
    if (key) next.add(key);
  }
  return next;
}
