import { getStoredString, setStoredString, removeStoredString } from "./storage";

export const PALETTE = ["#3b82f6", "#10b981", "#f59e0b", "#ef4444", "#8b5cf6", "#06b6d4"] as const;

export type NodeConnection = {
  id: string;
  name: string;
  url: string;
  color: string;
};

const NODES_KEY = "mindfs_nodes";
const ACTIVE_ID_KEY = "mindfs_active_node_id";
const AGGREGATED_KEY = "mindfs_aggregated";

function canUseStorage(): boolean {
  return typeof window !== "undefined";
}

function sanitizeURL(value: string): string {
  return String(value || "").trim().replace(/\/+$/, "");
}

function nextColor(existing: NodeConnection[]): string {
  return PALETTE[existing.length % PALETTE.length];
}

function genId(): string {
  try {
    if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") return crypto.randomUUID();
  } catch {}
  return `node_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`;
}

export function getNodes(): NodeConnection[] {
  const raw = getStoredString(NODES_KEY);
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed
      .map((item: any): NodeConnection | null => {
        if (!item || typeof item !== "object") return null;
        const id = String(item.id || "").trim();
        const name = String(item.name || "").trim();
        const url = sanitizeURL(String(item.url || ""));
        const color = String(item.color || "").trim() || "";
        if (!id || !name || !url) return null;
        return { id, name, url, color: color || "#3b82f6" };
      })
      .filter((x: any): x is NodeConnection => !!x);
  } catch {
    return [];
  }
}

export function setNodes(nodes: NodeConnection[]): void {
  setStoredString(NODES_KEY, JSON.stringify(nodes));
}

export function getActiveNodeId(): string | null {
  const v = getStoredString(ACTIVE_ID_KEY);
  return v ? String(v).trim() : null;
}

export function setActiveNodeId(id: string | null): void {
  if (!id) removeStoredString(ACTIVE_ID_KEY);
  else setStoredString(ACTIVE_ID_KEY, String(id).trim());
}

export function getActiveNode(): NodeConnection | null {
  const nodes = getNodes();
  if (!nodes.length) return null;
  const activeId = getActiveNodeId();
  if (activeId) {
    const found = nodes.find((n) => n.id === activeId);
    if (found) return found;
  }
  return nodes[0] || null;
}

export function getNodeById(id: string): NodeConnection | null {
  return getNodes().find((n) => n.id === String(id || "").trim()) || null;
}

export function addNode(input: { name: string; url: string; color?: string }): NodeConnection {
  const nodes = getNodes();
  const url = sanitizeURL(input.url);
  const name = String(input.name || "").trim() || new URL(url).hostname || url;
  const node: NodeConnection = {
    id: genId(),
    name,
    url,
    color: String(input.color || "").trim() || nextColor(nodes),
  };
  // dedup by url
  if (nodes.some((n) => n.url === url)) throw new Error("node_url_exists");
  const next = [...nodes, node];
  setNodes(next);
  if (!getActiveNodeId()) setActiveNodeId(node.id);
  return node;
}

export function updateNode(id: string, patch: Partial<Pick<NodeConnection, "name" | "url" | "color">>): NodeConnection | null {
  const nodes = getNodes();
  const idx = nodes.findIndex((n) => n.id === String(id || "").trim());
  if (idx < 0) return null;
  const cur = nodes[idx]!;
  const next: NodeConnection = {
    ...cur,
    name: patch.name !== undefined ? String(patch.name).trim() || cur.name : cur.name,
    url: patch.url !== undefined ? sanitizeURL(String(patch.url)) || cur.url : cur.url,
    color: patch.color !== undefined ? String(patch.color).trim() || cur.color : cur.color,
  };
  nodes[idx] = next;
  setNodes(nodes);
  return next;
}

export function removeNode(id: string): void {
  const nodes = getNodes();
  const next = nodes.filter((n) => n.id !== String(id || "").trim());
  setNodes(next);
  const activeId = getActiveNodeId();
  if (activeId === String(id || "").trim()) {
    setActiveNodeId(next[0]?.id || null);
  }
}

export function getAggregated(): boolean {
  const v = getStoredString(AGGREGATED_KEY);
  if (v === "0" || v === "false") return false;
  if (v === "1" || v === "true") return true;
  return true; // default aggregated
}

export function setAggregated(value: boolean): void {
  setStoredString(AGGREGATED_KEY, value ? "1" : "0");
}

// 迁移旧单节点配置：mindfs_api_base_url / mindfs_launcher_nodes
export function migrateLegacySingleBase(): NodeConnection | null {
  if (!canUseStorage()) return null;
  if (getNodes().length > 0) return getActiveNode();
  let legacyURL = "";
  let legacyName = "";
  try {
    legacyURL = sanitizeURL(String(window.localStorage.getItem("mindfs_api_base_url") || "").trim());
  } catch {}
  if (!legacyURL) {
    try {
      const raw = window.localStorage.getItem("mindfs_launcher_nodes");
      if (raw) {
        const parsed = JSON.parse(raw);
        if (Array.isArray(parsed) && parsed[0]?.url) {
          legacyURL = sanitizeURL(String(parsed[0].url));
          legacyName = String(parsed[0].name || "").trim();
        }
      }
    } catch {}
  }
  if (!legacyURL) {
    // fallback to origin if nothing legacy (single-node default)
    try {
      legacyURL = sanitizeURL(window.location.origin);
    } catch {}
  }
  if (!legacyURL) return null;
  const node: NodeConnection = {
    id: genId(),
    name: legacyName || (() => { try { return new URL(legacyURL).hostname; } catch { return legacyURL; } })(),
    url: legacyURL,
    color: PALETTE[0],
  };
  setNodes([node]);
  setActiveNodeId(node.id);
  return node;
}

export function cleanupLegacyKeys(): void {
  // keep legacy keys for now; caller can remove after migration verified
  // e2ee secrets are cleaned in bootstrap removal phase
}
