// 会话分组显示的节点色解析与审计摘要（纯函数，无浏览器依赖，可被 node:test 直导）
// 右面板 MultiProjectSessionList 的颜色源是 group._nodeColor；缺失时渲染侧回退 PALETTE[0]（#3b82f6 蓝）。
// 本模块统一解析链：byKey[nid::rid]._nodeColor → nodes 按 id → nodes 按 name → null（调用方决定回退）。

export type SessionGroupLike = {
  rootId?: string;
  rootName?: string;
  _nodeId?: string;
  _nodeColor?: string;
  _nodeName?: string;
  sessions?: unknown[];
};

export type NodeColorEntry = { id: string; name: string; color: string };

export const SESSION_GROUP_FALLBACK_BLUE = "#3b82f6";

export function resolveGroupColor(
  group: SessionGroupLike,
  byKey: Record<string, { _nodeColor?: string }>,
  nodes: NodeColorEntry[],
): string | null {
  const nid = String(group?._nodeId || "").trim();
  const rid = String(group?.rootId || "").trim();
  if (nid && rid) {
    const hit = byKey[`${nid}::${rid}`];
    const c = String(hit?._nodeColor || "").trim();
    if (c) return c;
  }
  if (nid) {
    const byId = (nodes || []).find((n) => String(n.id) === nid);
    if (byId?.color) return byId.color;
  }
  const nname = String(group?._nodeName || "").trim();
  if (nname) {
    const byName = (nodes || []).find((n) => String(n.name) === nname);
    if (byName?.color) return byName.color;
  }
  return null;
}

export function summarizeGroupsForLog(
  groups: SessionGroupLike[],
  byKey: Record<string, { _nodeColor?: string }>,
  nodes: NodeColorEntry[],
): Array<{ rootId: string; rootName: string; nid: string; color: string; sessions: number }> {
  return (groups || []).map((g) => {
    const color = resolveGroupColor(g, byKey, nodes);
    return {
      rootId: String(g?.rootId || "").trim(),
      rootName: String(g?.rootName || g?.rootId || "").trim(),
      nid: String(g?._nodeId || "").trim(),
      color: color || `FALLBACK${SESSION_GROUP_FALLBACK_BLUE}`,
      sessions: Array.isArray(g?.sessions) ? g!.sessions!.length : 0,
    };
  });
}
