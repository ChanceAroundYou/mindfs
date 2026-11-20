import { resolveProjectNodeId } from "../../shared/projectIdentity";

// 模块级「项目 → 机器」路由表，供 blob / 下载 / 服务层在没有 props 的地方自动选节点。
//
// 两套数据必须分开存：
//   - owners：注册表里这个 rootId 出现在**哪几台**机器上（同名项目可以有多台）。
//   - selected：用户当前点开的那台（选择优先于注册表）。
//
// 只有「当前选择」或「唯一归属」才算解析得出来。多台机器都有同一个 id 时返回
// undefined —— 让调用方显式带 nodeId，或者交给 active node 兜底，而不是按注册表
// 顺序猜一个：猜错时动作打到另一台机器，服务端回 `sql: no rows in result set`，
// 用户看到的是「既不能完成也不能删除」（见 G-NP / G-NQ）。
const owners = new Map<string, Set<string>>();
const selected = new Map<string, string>();

function seg(value: unknown): string {
  return String(value ?? "").trim();
}

export function setRootNodeId(rootId: string, nodeId?: string) {
  const rid = seg(rootId);
  if (!rid) return;
  const nid = seg(nodeId);
  if (nid) selected.set(rid, nid);
  else selected.delete(rid);
}

/**
 * 用注册表刷新归属表。
 *
 * 接受两种形状：完整索引 `nodeId::rootId → payload`（首选，带全部归属），
 * 以及只有唯一归属的 `rootId → payload`（改名等就地补丁的旧形状）。
 * 没带 `_nodeId` 的条目（零节点回落）不参与归属判断：它们只说明「本地有一份」。
 */
export function setRootNodeMap(record: Record<string, any>) {
  owners.clear();
  for (const [key, value] of Object.entries(record || {})) {
    const nid = seg((value as any)?._nodeId);
    if (!nid) continue;
    const idx = key.indexOf("::");
    const rid = seg(idx >= 0 ? key.slice(idx + 2) : key);
    if (!rid) continue;
    const set = owners.get(rid);
    if (set) set.add(nid);
    else owners.set(rid, new Set([nid]));
  }
}

/**
 * 解析裸 rootId 该发往哪台机器。
 *
 * `preferredNodeId` 是调用方手里已知的节点（列表条目 / 任务 / 会话自带的 `_nodeId`），
 * 永远最权威。没给时才退到「当前选择」→「唯一归属」。
 */
export function getRootNodeId(rootId: string, preferredNodeId?: string): string | undefined {
  const rid = seg(rootId);
  // 唯一的事实来源是 shared/projectIdentity 的判定规则；这里把本模块的两张表
  // 翻译成它的入参形状（"当前选择" = 这个 rootId 上次被选中的节点）。
  return resolveProjectNodeId({
    rootId: rid,
    explicitNodeId: preferredNodeId,
    currentRootId: rid,
    currentNodeId: selected.get(rid),
    nodesForRoot: (id) => {
      const set = owners.get(seg(id));
      return set ? Array.from(set) : [];
    },
  });
}

/** 这个 rootId 在注册表里被几台机器拥有（同名项目判断用）。 */
export function rootNodeOwners(rootId: string): string[] {
  const set = owners.get(seg(rootId));
  return set ? Array.from(set) : [];
}
