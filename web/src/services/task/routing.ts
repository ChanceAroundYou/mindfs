/**
 * 卡片动作该发给**哪台机器**。
 *
 * 任务自己带的 `_nodeId` 优先，其次才按项目 id 反查 —— 反查这张表每个 rootId
 * 只留得下一条（`indexManagedRoots` 的 byId 只保留首个），而**同名项目在多台机器上
 * 各有一份**是常态：fn 与 pc 都有 `docs`，两边任务号各自从 1 开始。
 * 只按 rootId 反查，fn 那张卡的动作就会打到 pc，服务端回 `sql: no rows in result set`，
 * 用户看到的是「既不能完成也不能删除」「删了还在工作台里」。
 */
export function taskActionNodeId(
  task: { _nodeId?: unknown } | null | undefined,
  rootId: string,
  getNodeIdForRoot: (rootId: string) => string | undefined,
): string | undefined {
  const own = String((task as any)?._nodeId || "").trim();
  if (own) return own;
  const rid = String(rootId || "").trim();
  if (!rid) return undefined;
  return String(getNodeIdForRoot(rid) || "").trim() || undefined;
}
