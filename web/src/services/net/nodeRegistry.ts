import { getStoredString, setStoredString, removeStoredString } from "../prefs/storage";
import { normalizeExplicitNodeBase, repairDuplicateDeployPrefix } from "./nodeBase";
import { DEPLOY_PREFIX } from "./prefix";
import { controlPath } from "./controlPlane";
import { DEFAULT_NODE_COLOR, PALETTE } from "../../shared/nodePalette";
export { DEFAULT_NODE_COLOR, PALETTE } from "../../shared/nodePalette";
import {
  LEGACY_SELF_NODE_ID,
  getSelfNodeId,
  getSelfNodeIdentity,
  initSelfNodeIdentity,
  isSelfNodeConnection,
} from "./selfNode";
export { LEGACY_SELF_NODE_ID, getSelfNodeId, getSelfNodeName, initSelfNodeIdentity, isSelfNode, isSelfNodeConnection } from "./selfNode";
import {
  deviceLocalNodeURL,
  makeSelfNode,
  nodeBaseKey,
  nodeRouteBase,
  normalizeAndDedup,
  normalizeRecord,
} from "./nodeTableNormalize";
import type { NodeConnection } from "./nodeTableNormalize";
export { nodeBaseKey, nodeRouteBase } from "./nodeTableNormalize";
export type { NodeConnection } from "./nodeTableNormalize";

const ACTIVE_ID_KEY = "mindfs_active_node_id";
const AGGREGATED_KEY = "mindfs_aggregated";
const LEGACY_NODES_KEY = "mindfs_nodes";

function nextColor(existing: NodeConnection[]): string {
  return PALETTE[existing.length % PALETTE.length];
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
  // 合成 self 行只在本机视角成立：表里已有别的行时把它写回去，服务端会按「同名不同 id」
  // 拒绝整表（边缘上就是共享 pc 行 + 合成 pc 行），改名/改色/学到的 instanceId 全部
  // 静默丢失。只有表本身就空（首次启动、只写本机这一行）时才让它落库。
  const stored = next.filter((node) => !node._syntheticSelf);
  const payload = (stored.length ? stored : next).map(({ _syntheticSelf, ...node }) => node);
  const res = await fetch(nodesApiPath(), {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
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
          const merged = normalizeAndDedup([...nodes, ...legacy.filter((n) => !isSelfNodeConnection(n))]);
          try {
            // 这是「服务端表为空」的种子写入：本机行必须一起落库（pushToServer 平时会
            // 把合成行剔除，这里显式去掉标记）。
            await enqueueServerWrite(merged.map((n) => ({ ...n, _syntheticSelf: undefined })));
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
  let self = nodes.find(isSelfNodeConnection) || null;
  if (!self) {
    self = makeSelfNode();
    // 追加而不是前插：首行留给控制面顺序里的节点（主控置顶由服务端保证）。
    nodes.push(self);
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
  // 本机行在共享表里可能挂着别的 id（边缘的 pc 行是随机 uuid）：旧 active id 等于
  // 本机自报 id 时迁到合并后那条行的 id，否则这条 id 解析不到节点、请求会退到页面服务器。
  if (getSelfNodeIdentity() && id === getSelfNodeId()) {
    const self = getNodes().find(isSelfNodeConnection);
    if (self && self.id !== id) {
      setStoredString(ACTIVE_ID_KEY, self.id);
      return self.id;
    }
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
  // 本机连接的寻址按当前 origin 推导（见 runtime.resolveNodeBaseURL / deviceLocalNodeURL），
  // 正是「页面所在这台机器」，拿不到 active id 时应该选它。
  const self = nodes.find(isSelfNodeConnection);
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
  // 判定必须用连接身份：边缘的本机行在共享表里是随机 uuid，只比 id 会放行删除。
  const nodes = getNodes();
  const target = nodes.find((n) => n.id === normalized);
  if (isSelfNodeConnection(target)) throw new Error("cannot_remove_self");
  const next = nodes.filter((n) => n.id !== normalized);
  await setNodes(next);
  const activeId = getActiveNodeId();
  if (activeId === normalized) {
    setActiveNodeId(next[0]?.id || null);
  }
}

/**
 * 上移/下移一行，整表写回（服务端按数组下标写 Order）。
 *
 * 首行固定：主控置顶是服务端保证的，客户端既不移动首行，也不让别的行挤到它前面。
 */
export async function moveNode(id: string, direction: "up" | "down"): Promise<void> {
  const normalized = String(id || "").trim();
  const nodes = getNodes();
  const index = nodes.findIndex((n) => n.id === normalized);
  if (index < 0) return;
  const target = direction === "up" ? index - 1 : index + 1;
  if (index === 0 || target < 1 || target >= nodes.length) return;
  const next = [...nodes];
  [next[index], next[target]] = [next[target]!, next[index]!];
  await setNodes(next);
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
