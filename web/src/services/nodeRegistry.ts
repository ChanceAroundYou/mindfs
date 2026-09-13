import { getStoredString, setStoredString, removeStoredString } from "./storage";
import { deriveLocalNodeBase, normalizeExplicitNodeBase, repairDuplicateDeployPrefix } from "./nodeBase";
import { DEPLOY_PREFIX } from "./prefix";
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

const ACTIVE_ID_KEY = "mindfs_active_node_id";
const AGGREGATED_KEY = "mindfs_aggregated";
const LEGACY_NODES_KEY = "mindfs_nodes";

function nextColor(existing: NodeConnection[]): string {
  return PALETTE[existing.length % PALETTE.length];
}

function localBaseURL(): string {
  try {
    const origin = typeof window !== "undefined" ? window.location.origin : "";
    return deriveLocalNodeBase(origin, DEPLOY_PREFIX);
  } catch {}
  return "";
}

// "local" 节点 = “当前设备”节点：其 URL 必须按当前设备推导，共享列表里持久化的 local URL
// 是某台设备写入的，对其它设备必然错误（实测：手机写入 local→home URL，PC 端解析
// nodeId="local" 全部路由到 home 服务器）。web 用当前 origin；原生壳 origin（capacitor://…）
// 不可直接 fetch，留空走相对路径/原生代理，天然指向当前连接的服务器。
function deviceLocalNodeURL(): string {
  const base = localBaseURL();
  if (!base || !/^https?:\/\//i.test(base)) return "";
  return base;
}

function makeLocalNode(): NodeConnection {
  return { id: LOCAL_NODE_ID, name: "local", url: deviceLocalNodeURL(), color: PALETTE[0] };
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
    if (prev.id === LOCAL_NODE_ID) continue;
    if (n.id === LOCAL_NODE_ID) seen.set(key, n);
  }
  return Array.from(seen.values());
}

// ---- in-memory only, server is source of truth (no localStorage cache for nodes) ----
let cache: NodeConnection[] | null = null;
let cachePromise: Promise<NodeConnection[]> | null = null;
let writeQueue: Promise<void> = Promise.resolve();

function normalizeRecord(item: any): NodeConnection | null {
  if (!item || typeof item !== "object") return null;
  const id = String(item.id || "").trim();
  const name = String(item.name || "").trim();
  const rawURL = normalizeExplicitNodeBase(String(item.url || ""));
  const url = repairDuplicateDeployPrefix(normalizeExplicitNodeBase(rawURL), DEPLOY_PREFIX);
  let color = String(item.color || "").trim() || "";
  if (!color) color = PALETTE[0];
  else if (!PALETTE.some((c) => c.toLowerCase() === color.toLowerCase())) {
    for (const p of paletteIndex) {
      const idx = p.findIndex((c) => c.toLowerCase() === color.toLowerCase());
      if (idx >= 0) { color = PALETTE[idx]!; break; }
    }
  }
  if (!id || !name || !url) return null;
  return { id, name, url, color };
}

function normalizeAndDedup(list: any[]): NodeConnection[] {
  let nodes = (Array.isArray(list) ? list : []).map(normalizeRecord).filter((x: any): x is NodeConnection => !!x);
  nodes = dedupNodesByOrigin(nodes as NodeConnection[]);
  const hasLocal = nodes.some((n) => n.id === LOCAL_NODE_ID);
  if (!hasLocal) {
    nodes = [makeLocalNode(), ...nodes];
  } else {
    const idx = nodes.findIndex((n) => n.id === LOCAL_NODE_ID);
    if (idx > 0) {
      const [local] = nodes.splice(idx, 1);
      nodes.unshift(local!);
    }
  }
  const local = nodes.find((n) => n.id === LOCAL_NODE_ID);
  // 读取时强制按当前设备推导（见 deviceLocalNodeURL）：共享列表持久化的 local URL 不可信
  if (local) local.url = deviceLocalNodeURL();
  return nodes;
}

function writeCache(next: NodeConnection[]) {
  const prev = cache;
  cache = next;
  try {
    // 只在列表真的变了才广播。syncNodesFromServer 每次都无条件走到这里，而 App 的
    // mindfs:nodes-changed 监听会重跑 refreshManagedRoots → syncNodesFromServer ——
    // 无脑广播会让两件事自激成死循环，且每轮都顺带打一次 /api/replying-sessions
    // 与 /api/sessions?multi_root=1。
    if (prev && JSON.stringify(prev) === JSON.stringify(next)) return;
    window.dispatchEvent(new CustomEvent("mindfs:nodes-changed"));
  } catch {}
}

function nodesApiPath(): string {
  return DEPLOY_PREFIX ? `${DEPLOY_PREFIX}/api/nodes` : "/api/nodes";
}

async function pushToServer(next: NodeConnection[]): Promise<void> {
  const res = await fetch(nodesApiPath(), {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(next),
  });
  if (!res.ok) throw new Error(`put nodes failed: ${res.status}`);
}

function enqueueServerWrite(next: NodeConnection[]): Promise<void> {
  const snapshot = next.map((node) => ({ ...node }));
  const write = writeQueue.then(() => pushToServer(snapshot));
  writeQueue = write.catch(() => {});
  return write;
}

function readLegacyLocalNodes(): NodeConnection[] | null {
  try {
    const raw = window.localStorage.getItem(LEGACY_NODES_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return null;
    const list = parsed.map(normalizeRecord).filter((x: any): x is NodeConnection => !!x) as NodeConnection[];
    return list.length ? list : null;
  } catch { return null; }
}
function clearLegacyLocalNodes() {
  try { window.localStorage.removeItem(LEGACY_NODES_KEY); } catch {}
  try { window.localStorage.removeItem("mindfs_nodes_synced"); } catch {}
}

export async function syncNodesFromServer(): Promise<NodeConnection[]> {
  if (cachePromise) return cachePromise;
  cachePromise = (async () => {
    const cacheAtStart = cache;
    try {
      const res = await fetch(nodesApiPath(), { cache: "no-store" });
      if (!res.ok) throw new Error(`fetch nodes failed: ${res.status}`);
      const remote = await res.json();
      const list = Array.isArray(remote) ? remote : [];
      const repairedRemoteURL = list.some((item) => {
        const rawURL = normalizeExplicitNodeBase(String(item?.url || ""));
        return rawURL !== repairDuplicateDeployPrefix(rawURL, DEPLOY_PREFIX);
      });
      let nodes = normalizeAndDedup(list);
      if (cache !== cacheAtStart) return cache || nodes;
      // one-time migration: server empty but old localStorage has nodes -> push once then clear legacy
      if (!list.length) {
        const legacy = readLegacyLocalNodes();
        if (legacy && legacy.length) {
          const merged = normalizeAndDedup([...nodes, ...legacy.filter((n) => n.id !== LOCAL_NODE_ID)]);
          try {
            await enqueueServerWrite(merged);
            nodes = merged;
            clearLegacyLocalNodes();
          } catch (error) {
            console.warn("[nodes] failed to migrate legacy nodes", error);
          }
        } else {
          // ensure at least local node is persisted so other devices see the same empty state
          try { await enqueueServerWrite(nodes); } catch (error) { console.warn("[nodes] failed to initialize server nodes", error); }
        }
      } else {
        if (repairedRemoteURL) await enqueueServerWrite(nodes);
        // server has data -> legacy cache is stale, drop it
        clearLegacyLocalNodes();
      }
      if (cache !== cacheAtStart) return cache || nodes;
      writeCache(nodes);
      return nodes;
    } catch (error) {
      console.warn("[nodes] failed to sync server nodes", error);
      if (cache) return cache;
      const local = [makeLocalNode()];
      writeCache(local);
      return local;
    } finally {
      cachePromise = null;
    }
  })();
  return cachePromise;
}

export function ensureLocalNode(): NodeConnection {
  const nodes = getNodes();
  let local = nodes.find((n) => n.id === LOCAL_NODE_ID) || null;
  if (!local) {
    local = makeLocalNode();
    nodes.unshift(local);
    void setNodes(nodes);
  } else if (!local.url) {
    local.url = deviceLocalNodeURL();
    void setNodes(nodes);
  }
  return local;
}

export function getNodes(): NodeConnection[] {
  if (cache) return [...cache];
  // no localStorage cache: before first sync, show ephemeral local node only
  return [makeLocalNode()];
}

export async function setNodes(nodes: NodeConnection[]): Promise<void> {
  const normalized = normalizeAndDedup(nodes as any);
  writeCache(normalized);
  await enqueueServerWrite(normalized);
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

export async function addNode(input: { name: string; url: string; color?: string }): Promise<NodeConnection> {
  const nodes = getNodes();
  const url = normalizeExplicitNodeBase(input.url);
  const name = String(input.name || "").trim() || (() => { try { return new URL(url).hostname; } catch { return url; } })();
  const node: NodeConnection = {
    id: genId(),
    name,
    url,
    color: String(input.color || "").trim() || nextColor(nodes),
  };
  if (nodes.some((n) => nodeOriginKey(n.url) === nodeOriginKey(url))) throw new Error("node_url_exists");
  const next = [...nodes, node];
  await setNodes(next);
  if (!getActiveNodeId()) setActiveNodeId(node.id);
  return node;
}

export async function updateNode(id: string, patch: Partial<Pick<NodeConnection, "name" | "url" | "color">>): Promise<NodeConnection | null> {
  const nodes = getNodes();
  const idx = nodes.findIndex((n) => n.id === String(id || "").trim());
  if (idx < 0) return null;
  const cur = nodes[idx]!;
  const next: NodeConnection = {
    ...cur,
    name: patch.name !== undefined ? String(patch.name).trim() || cur.name : cur.name,
    url: patch.url !== undefined ? normalizeExplicitNodeBase(String(patch.url)) || cur.url : cur.url,
    color: patch.color !== undefined ? String(patch.color).trim() || cur.color : cur.color,
  };
  nodes[idx] = next;
  await setNodes(nodes);
  return next;
}

export async function removeNode(id: string): Promise<void> {
  const normalized = String(id || "").trim();
  if (normalized === LOCAL_NODE_ID) throw new Error("cannot_remove_local");
  const nodes = getNodes();
  const next = nodes.filter((n) => n.id !== normalized);
  await setNodes(next);
  const activeId = getActiveNodeId();
  if (activeId === normalized) {
    setActiveNodeId(next[0]?.id || null);
  }
}

export function getAggregated(): boolean {
  const v = getStoredString(AGGREGATED_KEY);
  if (v === "0" || v === "false") return false;
  if (v === "1" || v === "true") return true;
  return true;
}

export function setAggregated(value: boolean): void {
  setStoredString(AGGREGATED_KEY, value ? "1" : "0");
}

export function migrateLegacySingleBase(): NodeConnection | null {
  try { void syncNodesFromServer(); } catch {}
  return getActiveNode();
}

export function cleanupLegacyKeys(): void {}

export function applyNodesFromServer(list: any[]): void {
  const nodes = normalizeAndDedup(Array.isArray(list) ? list : []);
  writeCache(nodes);
}

function genId(): string {
  try {
    if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") return crypto.randomUUID();
  } catch {}
  return `node_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`;
}
