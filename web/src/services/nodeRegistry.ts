import { getStoredString, setStoredString, removeStoredString } from "./storage";

export const PALETTE = ["#3b82f6", "#f59e0b", "#7c6bd6", "#c66a7a", "#8a8f99", "#7aae8a"] as const;
const PREVIOUS_PALETTE = ["#7c6bd6", "#c9b84a", "#6a8dc2", "#c66a7a", "#8a8f99", "#7aae8a"] as const;
const OLD_PALETTE = ["#6d5bcf", "#0ea5a0", "#e07a2f", "#2f8f4e", "#d9466a", "#7a9a3a"] as const;
const paletteIndex = [PREVIOUS_PALETTE, OLD_PALETTE] as const;
export const LOCAL_NODE_ID = "local";

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

// 反代子路径前缀：当前页面挂在子路径下（如 /mindfs）时，裸 origin 的节点 URL 补上前缀，
// 使请求落到 host.domain/mindfs/api/... 而非 404 的 host.domain/api/...
function detectReverseProxyPrefix(): string {
  if (typeof window === "undefined") return "";
  try {
    const segments = (window.location.pathname || "/").split("/").filter(Boolean);
    return segments.length ? `/${segments[0]}` : "";
  } catch {
    return "";
  }
}

function hasURIPath(input: string): boolean {
  try {
    const u = new URL(input);
    return u.pathname.replace(/\/+$/, "") !== "";
  } catch {
    return false;
  }
}

function normalizeNodeURL(input: string): string {
  const base = sanitizeURL(input);
  if (!base) return "";
  if (hasURIPath(base)) {
    return base;
  }
  const prefix = detectReverseProxyPrefix();
  if (!prefix) {
    return base;
  }
  return `${base}${prefix}`;
}

function nextColor(existing: NodeConnection[]): string {
  return PALETTE[existing.length % PALETTE.length];
}

function localBaseURL(): string {
  try {
    const origin = typeof window !== "undefined" ? window.location.origin : "";
    if (origin) return normalizeNodeURL(sanitizeURL(origin));
  } catch {}
  return "";
}

function makeLocalNode(): NodeConnection {
  return { id: LOCAL_NODE_ID, name: "local", url: localBaseURL(), color: PALETTE[0] };
}

export function ensureLocalNode(): NodeConnection {
  const nodes = getNodesRaw();
  let local = nodes.find((n) => n.id === LOCAL_NODE_ID) || null;
  if (!local) {
    local = makeLocalNode();
    nodes.unshift(local);
    setNodes(nodes);
  } else if (!local.url) {
    local.url = localBaseURL();
    setNodes(nodes);
  }
  return local;
}


function nodeOriginKey(url: string): string {
  try {
    const u = new URL(url);
    return `${u.protocol}//${u.host}`.toLowerCase();
  } catch { return url.toLowerCase(); }
}
function dedupNodesByOrigin(nodes: NodeConnection[]): NodeConnection[] {
  const seen = new Map<string, NodeConnection>();
  for (const n of nodes) {
    const key = nodeOriginKey(n.url);
    const prev = seen.get(key);
    if (!prev) { seen.set(key, n); continue; }
    // keep local over non-local, otherwise keep first
    if (prev.id === LOCAL_NODE_ID) continue;
    if (n.id === LOCAL_NODE_ID) seen.set(key, n);
  }
  return Array.from(seen.values());
}
function getNodesRaw(): NodeConnection[] {
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
        const rawURL = sanitizeURL(String(item.url || ""));
        const url = normalizeNodeURL(rawURL);
        const color = String(item.color || "").trim() || "";
        if (!id || !name || !url) return null;
        return { id, name, url, color: color || PALETTE[0] };
      })
      .filter((x: any): x is NodeConnection => !!x);
  } catch {
    return [];
  }
}

function genId(): string {
  try {
    if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") return crypto.randomUUID();
  } catch {}
  return `node_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`;
}

export function getNodes(): NodeConnection[] {
  const raw = getStoredString(NODES_KEY);
  let parsed: any[] | null = null;
  if (raw) {
    try {
      const p = JSON.parse(raw);
      parsed = Array.isArray(p) ? p : null;
    } catch {
      parsed = null;
    }
  }
  if (parsed === null) {
    // 首次：写入 local
    const local = makeLocalNode();
    if (local.url) {
      setStoredString(NODES_KEY, JSON.stringify([local]));
      try { window.dispatchEvent(new CustomEvent("mindfs:nodes-changed")); } catch {}
      return [local];
    }
    return [local];
  }
  let changed = false;
  let nodes = parsed
    .map((item: any): NodeConnection | null => {
      if (!item || typeof item !== "object") return null;
      const id = String(item.id || "").trim();
      const name = String(item.name || "").trim();
      const rawURL = sanitizeURL(String(item.url || ""));
      const url = normalizeNodeURL(rawURL);
      if (url !== rawURL) changed = true;
      let color = String(item.color || "").trim() || "";
      if (!color) color = PALETTE[0];
      else if (!PALETTE.some((c) => c.toLowerCase() === color.toLowerCase())) {
        // 已是当前调色板色（如 #7c6bd6）则保留；否则按历史调色板位置迁移
        for (const p of paletteIndex) {
          const idx = p.findIndex((c) => c.toLowerCase() === color.toLowerCase());
          if (idx >= 0) {
            color = PALETTE[idx]!;
            changed = true;
            break;
          }
        }
      }
      if (!id || !name || !url) return null;
      return { id, name, url, color };
    })
    .filter((x: any): x is NodeConnection => !!x);
  // 去重：同 origin 的节点仅保留 local 优先的一条
  const beforeDedupLen = nodes.length;
  nodes = dedupNodesByOrigin(nodes as NodeConnection[]);
  if (nodes.length !== beforeDedupLen) changed = true;
  // 保证 local 首位存在，不可删
  const hasLocal = nodes.some((n) => n.id === LOCAL_NODE_ID);
  if (!hasLocal) {
    nodes = [makeLocalNode(), ...nodes];
    changed = true;
  } else {
    // local 固定首位
    const idx = nodes.findIndex((n) => n.id === LOCAL_NODE_ID);
    if (idx > 0) {
      const [local] = nodes.splice(idx, 1);
      nodes.unshift(local!);
      changed = true;
    }
  }
  if (changed && canUseStorage()) {
    setStoredString(NODES_KEY, JSON.stringify(nodes));
    try { window.dispatchEvent(new CustomEvent("mindfs:nodes-changed")); } catch {}
  }
  return nodes;
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
  const url = normalizeNodeURL(input.url);
  const name = String(input.name || "").trim() || new URL(url).hostname || url;
  const node: NodeConnection = {
    id: genId(),
    name,
    url,
    color: String(input.color || "").trim() || nextColor(nodes),
  };
  // dedup by origin (same host/port -> same backend)
  if (nodes.some((n) => nodeOriginKey(n.url) === nodeOriginKey(url))) throw new Error("node_url_exists");
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
    url: patch.url !== undefined ? normalizeNodeURL(String(patch.url)) || cur.url : cur.url,
    color: patch.color !== undefined ? String(patch.color).trim() || cur.color : cur.color,
  };
  nodes[idx] = next;
  setNodes(nodes);
  return next;
}

export function removeNode(id: string): void {
  const normalized = String(id || "").trim();
  if (normalized === LOCAL_NODE_ID) throw new Error("cannot_remove_local");
  const nodes = getNodes();
  const next = nodes.filter((n) => n.id !== normalized);
  setNodes(next);
  const activeId = getActiveNodeId();
  if (activeId === normalized) {
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

// 迁移旧单节点配置：mindfs_api_base_url / mindfs_launcher_nodes → 保留为非 local 节点
export function migrateLegacySingleBase(): NodeConnection | null {
  if (!canUseStorage()) return ensureLocalNode();
  // 保证 local 存在
  ensureLocalNode();
  // 仅当除 local 外无节点时，尝试把旧单节点迁移为第二节点
  const nodes = getNodes();
  if (nodes.length > 1) return getActiveNode();
  let legacyURL = "";
  let legacyName = "";
  try {
    legacyURL = normalizeNodeURL(String(window.localStorage.getItem("mindfs_api_base_url") || "").trim());
  } catch {}
  if (!legacyURL) {
    try {
      const raw = window.localStorage.getItem("mindfs_launcher_nodes");
      if (raw) {
        const parsed = JSON.parse(raw);
        if (Array.isArray(parsed) && parsed[0]?.url) {
          legacyURL = normalizeNodeURL(String(parsed[0].url));
          legacyName = String(parsed[0].name || "").trim();
        }
      }
    } catch {}
  }
  if (!legacyURL) return getActiveNode();
  const localURL = makeLocalNode().url;
  if (nodeOriginKey(legacyURL) === nodeOriginKey(localURL)) return getActiveNode();
  if (nodes.some((n) => nodeOriginKey(n.url) === nodeOriginKey(legacyURL))) return getActiveNode();
  const node: NodeConnection = {
    id: genId(),
    name: legacyName || (() => { try { return new URL(legacyURL).hostname; } catch { return legacyURL; } })(),
    url: legacyURL,
    color: PALETTE[nodes.length % PALETTE.length],
  };
  setNodes([...nodes, node]);
  return node;
}

export function cleanupLegacyKeys(): void {
  // keep legacy keys for now; caller can remove after migration verified
  // e2ee secrets are cleaned in bootstrap removal phase
}
