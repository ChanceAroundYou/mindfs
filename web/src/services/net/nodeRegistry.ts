import { getStoredString, setStoredString, removeStoredString } from "../prefs/storage";
import { deriveLocalNodeBase, normalizeExplicitNodeBase, repairDuplicateDeployPrefix } from "./nodeBase";
import { DEPLOY_PREFIX } from "./prefix";
import { controlPath } from "./controlPlane";
import { DEFAULT_NODE_COLOR, PALETTE } from "../../shared/nodePalette";
export { DEFAULT_NODE_COLOR, PALETTE } from "../../shared/nodePalette";
import {
  LEGACY_SELF_NODE_ID,
  getSelfNodeId,
  getSelfNodeIdentity,
  getSelfNodeName,
  initSelfNodeIdentity,
  isSelfNode,
} from "./selfNode";
export { LEGACY_SELF_NODE_ID, getSelfNodeId, getSelfNodeName, initSelfNodeIdentity, isSelfNode } from "./selfNode";
const PREVIOUS_PALETTE = ["#7c6bd6", "#c9b84a", "#6a8dc2", "#c66a7a", "#8a8f99", "#7aae8a"] as const;
const OLD_PALETTE = ["#6d5bcf", "#0ea5a0", "#e07a2f", "#2f8f4e", "#d9466a", "#7a9a3a"] as const;
const paletteIndex = [PREVIOUS_PALETTE, OLD_PALETTE] as const;
export type NodeConnection = {
  id: string;
  name: string;
  /** 直连基址（用户可填，可为空）。 */
  url: string;
  color: string;
  /** 主控替这台机器转发的兜底基址（<主控>/relay/<name>）。url 为空时才用它。 */
  relayUrl?: string;
  /** 这台设备的控制面身份（edge 的 instance_id）。主控据此把 relay 并回同一个节点。 */
  instanceId?: string;
  /** 这是**另一台机器**上的节点。只用于展示与探测，不参与寻址。 */
  remote?: boolean;
};

// 节点实际可用的基址：直连优先、relay 兜底。寻址、去重、探活都必须走这里，
// 否则「只有 relayUrl 的 relay-only 节点」会被当成没有地址而丢掉。
export function nodeRouteBase(
  node: { url?: string; relayUrl?: string } | null | undefined,
): string {
  if (!node) return "";
  return (node.url || "").trim() || (node.relayUrl || "").trim();
}

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

// self 节点 = “当前这台机器”：其 URL 必须按当前设备推导，共享列表里持久化的
// self URL 是某台设备写入的，对其它设备必然错误（实测：手机写入 local→home URL，
// PC 端解析 nodeId="local" 全部路由到 home 服务器）。web 用当前 origin；原生壳
// origin（capacitor://…）不可直接 fetch，留空走相对路径/原生代理。
function deviceLocalNodeURL(): string {
  const base = localBaseURL();
  if (!base || !/^https?:\/\//i.test(base)) return "";
  return base;
}

function makeSelfNode(): NodeConnection {
  return { id: getSelfNodeId(), name: getSelfNodeName(), url: deviceLocalNodeURL(), color: PALETTE[0] };
}

// 去重键必须带上路径段：同一台物理机可以挂两个部署前缀（实测 PC 上 WSL 节点走 /mindfs、
// Windows 节点走 /mindfs-win，两者共用 pc.xiaokubao.space）。只比 origin 会把后一个节点吞掉
// —— 界面里看不到它，「添加节点」还会报 node_url_exists。
// origin 那部分保留：它负责把共享列表里持久化的 local URL 与当前设备推导的那条并成一条。
export function nodeBaseKey(url: string): string {
  try {
    const u = new URL(url);
    const path = u.pathname.replace(/\/+$/, "");
    return `${u.protocol}//${u.host}${path}`.toLowerCase();
  } catch { return url.toLowerCase(); }
}
function dedupNodesByOrigin(nodes: NodeConnection[]): NodeConnection[] {
  const seen = new Map<string, NodeConnection>();
  for (const n of nodes) {
    const key = nodeBaseKey(nodeRouteBase(n));
    const prev = seen.get(key);
    if (!prev) { seen.set(key, n); continue; }
    // 同一 origin 上已有条目时，self 行必须留下：它的 URL 随后会按当前设备重算，
    // 别的设备写进去的 URL 不能信。
    if (isSelfNode(prev.id)) continue;
    if (isSelfNode(n.id)) seen.set(key, n);
  }
  return Array.from(seen.values());
}

// relay 是同一节点上的第二通道：同 instanceId 的直连行与 relay-only 行并成一行，
// 直连优先、把 relayUrl 带过去。服务端 normalize 也做同样的合并；这里先保证界面
// 立刻一致，不用等下一次整表同步。
function dedupNodesByInstance(nodes: NodeConnection[]): NodeConnection[] {
  const index = new Map<string, number>();
  const out: NodeConnection[] = [];
  for (const node of nodes) {
    const iid = (node.instanceId || "").trim();
    if (!iid) { out.push(node); continue; }
    const i = index.get(iid);
    if (i === undefined) { index.set(iid, out.length); out.push(node); continue; }
    const prim = out[i]!;
    const relayUrl = prim.relayUrl || node.relayUrl;
    if (!prim.url && node.url) {
      out[i] = { ...node, relayUrl, color: node.color || prim.color, remote: prim.remote || node.remote };
    } else {
      out[i] = { ...prim, relayUrl, remote: prim.remote || node.remote };
    }
  }
  return out;
}

// ---- in-memory only, server is source of truth (no localStorage cache for nodes) ----
let cache: NodeConnection[] | null = null;
let cachePromise: Promise<NodesSyncOutcome> | null = null;
let writeQueue: Promise<void> = Promise.resolve();

/**
 * 一次节点表同步「到底拉到了没有」。
 *
 * 不能只看返回值：拉不到时这里会退回缓存、再退回本地兜底，而**返回值长得一模一样**。
 * 调用方拿这个数组去判断成败就会把「失败」读成「成功」—— 界面于是拿一份可能已过时的
 * 清单，理直气壮地宣布「所有节点都连不上」，而不是说「这份清单本身不可信」。
 */
export type NodesSyncOutcome = {
  nodes: NodeConnection[];
  /** `server` = 真的从主节点拉到了；另两种都是失败后的兜底。 */
  source: "server" | "cache" | "local-fallback";
  error?: unknown;
};

function normalizeRecord(item: any): NodeConnection | null {
  if (!item || typeof item !== "object") return null;
  let id = String(item.id || "").trim();
  let name = String(item.name || "").trim();
  // 旧伪节点 local 一律映射成自报身份：节点表是共享的，主控与边缘过去都往里写过
  // 一个叫 local 的行；换成 home/pc-win 后，下一次整表写回就把它替换掉。
  if (id === LEGACY_SELF_NODE_ID) {
    const self = getSelfNodeIdentity();
    if (self) {
      id = self.id;
      name = self.name;
    }
  }
  const rawURL = normalizeExplicitNodeBase(String(item.url || ""));
  const url = rawURL ? repairDuplicateDeployPrefix(rawURL, DEPLOY_PREFIX) : "";
  const rawRelay = normalizeExplicitNodeBase(String(item.relayUrl || ""));
  const relayUrl = rawRelay ? repairDuplicateDeployPrefix(rawRelay, DEPLOY_PREFIX) : "";
  const instanceId = String(item.instanceId || "").trim();
  let color = String(item.color || "").trim() || "";
  if (!color) color = PALETTE[0];
  else if (!PALETTE.some((c) => c.toLowerCase() === color.toLowerCase())) {
    for (const p of paletteIndex) {
      const idx = p.findIndex((c) => c.toLowerCase() === color.toLowerCase());
      if (idx >= 0) { color = PALETTE[idx]!; break; }
    }
  }
  // 旧实现的 relay:* 独立节点行一律丢弃（与后端 normalize 同口径）：它已经被
  // 合并成同一节点的 relayUrl，留着就是同一台机器的第二个节点。
  if (id.startsWith("relay:")) return null;
  if (!id || !name || (!url && !relayUrl)) return null;
  return {
    id,
    name,
    url,
    color,
    relayUrl: relayUrl || undefined,
    instanceId: instanceId || undefined,
    remote: !!item.remote,
  };
}

function dedupNodesByID(nodes: NodeConnection[]): NodeConnection[] {
  const seen = new Map<string, NodeConnection>();
  for (const node of nodes) {
    const id = String(node.id || "").trim();
    if (!id) continue;
    // 同 id 两条（例如旧 local 行映射后撞上已有的 home 行）保留 self 那条：
    // 它的 URL 是设备推导的，另一条的 URL 是别的设备写下的。
    if (!seen.has(id) || isSelfNode(id)) seen.set(id, node);
  }
  return Array.from(seen.values());
}

function normalizeAndDedup(list: any[]): NodeConnection[] {
  let nodes = (Array.isArray(list) ? list : []).map(normalizeRecord).filter((x: any): x is NodeConnection => !!x);
  nodes = dedupNodesByOrigin(nodes as NodeConnection[]);
  nodes = dedupNodesByInstance(nodes as NodeConnection[]);
  nodes = dedupNodesByID(nodes as NodeConnection[]);
  const hasSelf = nodes.some((n) => isSelfNode(n.id));
  if (!hasSelf) {
    nodes = [makeSelfNode(), ...nodes];
  } else {
    const idx = nodes.findIndex((n) => isSelfNode(n.id));
    if (idx > 0) {
      const [self] = nodes.splice(idx, 1);
      nodes.unshift(self!);
    }
  }
  const self = nodes.find((n) => isSelfNode(n.id));
  // 读取时强制按当前设备推导（见 deviceLocalNodeURL）：共享列表持久化的 self URL 不可信
  if (self) self.url = deviceLocalNodeURL();
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
  // 节点表是控制面：只有主节点一份真相，请求必须打页面服务器而不是当前选中节点。
  // 见 docs/multi-node-control-plane.md。
  return controlPath("/api/nodes");
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
  return (await syncNodesFromServerWithOutcome()).nodes;
}

/**
 * 和 `syncNodesFromServer` 同一条请求，但把「拉到了没有」一并带回来。
 *
 * 两个入口共用同一个在途 promise：健康状态需要知道成败，而绝大多数调用方只想要列表，
 * 让它们各发一次请求是没必要的。调用方拿到 `source` 之后再决定记什么。
 */
export async function syncNodesFromServerWithOutcome(): Promise<NodesSyncOutcome> {
  if (cachePromise) return cachePromise;
  cachePromise = (async (): Promise<NodesSyncOutcome> => {
    const cacheAtStart = cache;
    try {
      // 先拿本机身份：节点表的去重、旧 local 行的映射、作用域键都依赖它。
      // 失败不阻塞同步（自报身份拿不到时退回旧 id，行为与改造前一致）。
      await initSelfNodeIdentity().catch(() => null);
      const res = await fetch(nodesApiPath(), { cache: "no-store" });
      if (!res.ok) throw new Error(`fetch nodes failed: ${res.status}`);
      const remote = await res.json();
      const list = Array.isArray(remote) ? remote : [];
      // 旧表中可能还留着 local 行；内存里已映射成自报 id，顺手写回让其它设备也看到新 id。
      const hasLegacySelfRow = list.some((item) => String(item?.id || "").trim() === LEGACY_SELF_NODE_ID);
      const repairedRemoteURL = list.some((item) => {
        const rawURL = normalizeExplicitNodeBase(String(item?.url || ""));
        return rawURL !== repairDuplicateDeployPrefix(rawURL, DEPLOY_PREFIX);
      });
      let nodes = normalizeAndDedup(list);
      if (cache !== cacheAtStart) return { nodes: cache || nodes, source: "server" };
      // one-time migration: server empty but old localStorage has nodes -> push once then clear legacy
      if (!list.length) {
        const legacy = readLegacyLocalNodes();
        if (legacy && legacy.length) {
          const merged = normalizeAndDedup([...nodes, ...legacy.filter((n) => !isSelfNode(n.id))]);
          try {
            await enqueueServerWrite(merged);
            nodes = merged;
            clearLegacyLocalNodes();
          } catch (error) {
            console.warn("[nodes] failed to migrate legacy nodes", error);
          }
        } else {
          // ensure at least self node is persisted so other devices see the same empty state
          try { await enqueueServerWrite(nodes); } catch (error) { console.warn("[nodes] failed to initialize server nodes", error); }
        }
      } else {
        if (repairedRemoteURL || hasLegacySelfRow) await enqueueServerWrite(nodes);
        // server has data -> legacy cache is stale, drop it
        clearLegacyLocalNodes();
      }
      if (cache !== cacheAtStart) return { nodes: cache || nodes, source: "server" };
      writeCache(nodes);
      return { nodes, source: "server" };
    } catch (error) {
      console.warn("[nodes] failed to sync server nodes", error);
      if (cache) return { nodes: cache, source: "cache", error };
      const fallback = [makeSelfNode()];
      writeCache(fallback);
      return { nodes: fallback, source: "local-fallback", error };
    } finally {
      cachePromise = null;
    }
  })();
  return cachePromise;
}

export function ensureSelfNode(): NodeConnection {
  const nodes = getNodes();
  let self = nodes.find((n) => isSelfNode(n.id)) || null;
  if (!self) {
    self = makeSelfNode();
    nodes.unshift(self);
    void setNodes(nodes);
  } else if (!self.url) {
    self.url = deviceLocalNodeURL();
    void setNodes(nodes);
  }
  return self;
}

export function getNodes(): NodeConnection[] {
  if (cache) return [...cache];
  // no localStorage cache: before first sync, show ephemeral self node only
  return [makeSelfNode()];
}

export async function setNodes(nodes: NodeConnection[]): Promise<void> {
  const normalized = normalizeAndDedup(nodes as any);
  writeCache(normalized);
  await enqueueServerWrite(normalized);
}

/**
 * 记录某节点自报的控制面身份（探活时从 /health 的 instanceId 学到）。
 *
 * 只在值真的变化时整表写回一次：主控据此把 relay 通道并回这台机器的既有节点行。
 * 返回是否真的触发了写入。空 id 或已一致都返回 false（探活每次都会调，不能每次都写）。
 */
export function learnNodeInstanceId(nodeId: string, instanceId: string): boolean {
  const iid = String(instanceId || "").trim();
  const id = String(nodeId || "").trim();
  if (!iid || !id) return false;
  const list = getNodes();
  const node = list.find((n) => n.id === id);
  if (!node || (node.instanceId || "") === iid) return false;
  const next = list.map((n) => (n.id === id ? { ...n, instanceId: iid } : n));
  void setNodes(next);
  return true;
}

export function getActiveNodeId(): string | null {
  const v = getStoredString(ACTIVE_ID_KEY);
  if (!v) return null;
  const id = String(v).trim();
  // 兼容读：旧值 local = 本机。知道自报身份后顺手改写，避免每次启动都要映射一次。
  if (id === LEGACY_SELF_NODE_ID && getSelfNodeIdentity()) {
    const selfId = getSelfNodeId();
    setStoredString(ACTIVE_ID_KEY, selfId);
    return selfId;
  }
  return id;
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
  // 存的 active id 解析不出来（首次同步前被清、节点被删/重加换了 id、或从另一台
  // 设备带来的旧 id）时**不要退到 nodes[0]** —— 那可能正是另一台机器，于是所有
  // 不带 nodeId 的请求（appURL(undefined) → getApiBaseURL → getActiveNode）
  // 会静默打到那台机器上：页面明明开在本机，会话却读的是 pc 的数据，
  // 而且两边 id/名字都一样，肉眼完全看不出来。
  // self 节点的 URL 是按当前 origin 推导的（见 deviceLocalNodeURL），正是
  // 「页面所在这台机器」，拿不到 active id 时应该选它。
  const self = nodes.find((n) => isSelfNode(n.id));
  if (self) return self;
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
  if (nodes.some((n) => nodeBaseKey(nodeRouteBase(n)) === nodeBaseKey(url))) throw new Error("node_url_exists");
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
  // 直连是可选项：有 relayUrl 兜底时允许把 url 清空（回落 relay）。
  // 没有兜底通道还清空 URL，这条行就没有地址了 —— normalizeRecord 会把它丢掉，
  // 那样「保存一次」等于「删掉这个节点」，所以保留原值。
  let nextUrl = cur.url;
  if (patch.url !== undefined) {
    const candidate = normalizeExplicitNodeBase(String(patch.url));
    if (candidate) nextUrl = candidate;
    else if (cur.relayUrl) nextUrl = "";
  }
  const next: NodeConnection = {
    ...cur,
    name: patch.name !== undefined ? String(patch.name).trim() || cur.name : cur.name,
    url: nextUrl,
    color: patch.color !== undefined ? String(patch.color).trim() || cur.color : cur.color,
  };
  nodes[idx] = next;
  await setNodes(nodes);
  return next;
}

export async function removeNode(id: string): Promise<void> {
  const normalized = String(id || "").trim();
  // 本机不是「可删除的节点」：它由当前服务器与运行设备决定，删掉只会在下次同步时回来。
  if (isSelfNode(normalized)) throw new Error("cannot_remove_self");
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
