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
