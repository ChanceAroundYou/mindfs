// ponytail: minimal global root->nodeId map so blob/download/services can auto-route without prop drilling
const map = new Map<string, string>();

export function setRootNodeId(rootId: string, nodeId?: string) {
  const rid = String(rootId || "").trim();
  if (!rid) return;
  const nid = String(nodeId || "").trim();
  if (nid) map.set(rid, nid);
  else map.delete(rid);
}

export function setRootNodeMap(next: Record<string, any>) {
  map.clear();
  for (const [k, v] of Object.entries(next || {})) {
    const nid = String((v as any)?._nodeId || "").trim();
    if (nid) map.set(String(k), nid);
  }
}

export function getRootNodeId(rootId: string): string | undefined {
  const v = map.get(String(rootId || "").trim());
  return v || undefined;
}
