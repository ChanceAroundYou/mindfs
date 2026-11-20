/**
 * 项目身份 = (`nodeId`, `rootId`)。
 *
 * `root_id` 是**一台机器内部**的项目 id（常常就是目录名或绝对路径）。两台机器各有一个
 * 同名项目是常态 —— 实测 fn 与 pc 都有 `docs`，任务号各自从 1 开始。所以裸 rootId
 * 只在两种情况下足以定位项目：
 *
 *   1. 它就是当前选中的项目（`currentRootId` + `currentRootNodeId`）；
 *   2. 全表只有一台机器拥有它。
 *
 * 其余情况必须由调用方带显式 nodeId。解析不出来时返回 `undefined` —— 宁可交给
 * active node 兜底并在日志里暴露，也不要「首个获胜」：按第一台机器猜，猜错时
 * 服务端回 `sql: no rows in result set`，用户看到的是「既不能完成也不能删除」。
 */

export type ResolveProjectNodeInput = {
  rootId: string | null | undefined;
  /** 调用方自己知道的节点（列表条目、任务、会话上带的 `_nodeId`）。最权威。 */
  explicitNodeId?: string | null;
  /** 当前选中的项目 id 与它所在的节点。 */
  currentRootId?: string | null;
  currentNodeId?: string | null;
  /** 注册表里拥有这个 rootId 的节点（去重前后都行，函数自己会去重）。 */
  nodesForRoot: (rootId: string) => readonly string[];
};

function seg(value: string | null | undefined): string {
  return String(value ?? "").trim();
}

export function resolveProjectNodeId(input: ResolveProjectNodeInput): string | undefined {
  const explicit = seg(input.explicitNodeId);
  if (explicit) return explicit;
  const rid = seg(input.rootId);
  if (!rid) return undefined;
  const curRid = seg(input.currentRootId);
  const curNid = seg(input.currentNodeId);
  if (curRid && rid === curRid && curNid) return curNid;
  const owners = Array.from(new Set((input.nodesForRoot(rid) || []).map(seg).filter(Boolean)));
  return owners.length === 1 ? owners[0] : undefined;
}
