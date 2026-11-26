// 节点表的规整与去重（纯数据变换：不发请求、不碰缓存）。
//
// 控制面返回的列表在进界面前要过三道去重：
//   1. 同一地址（origin + 部署前缀）只留一条 —— 另一台设备写进共享表的本机地址，
//      会与当前设备推导出的地址重合；
//   2. 同一 instanceId 的直连行与 relay-only 行并成一条（relay 是同节点的第二通道）；
//   3. 旧 local 行映射成自报身份后可能撞上已有行，按 id 再并一次。
// 本机行的两条约定也在这里：合成行追加在末尾（不抢首行，主控置顶由服务端保证），
// 共享行保留自己的直连网址（统一后的 pc 靠这条留住 /mindfs-win）。
import { deriveLocalNodeBase, normalizeExplicitNodeBase, repairDuplicateDeployPrefix } from "./nodeBase";
import { DEPLOY_PREFIX } from "./prefix";
import { PALETTE } from "../../shared/nodePalette";
import {
  LEGACY_SELF_NODE_ID,
  getSelfNodeId,
  getSelfNodeIdentity,
  getSelfNodeName,
  isSelfNodeConnection,
} from "./selfNode";

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
  /** 合成出来的本机行（控制面表里还没有这台机器）。只在表为空时写回服务端。 */
  _syntheticSelf?: boolean;
};

// 节点实际可用的基址：直连优先、relay 兜底。寻址、去重、探活都必须走这里，
// 否则「只有 relayUrl 的 relay-only 节点」会被当成没有地址而丢掉。
export function nodeRouteBase(
  node: { url?: string; relayUrl?: string } | null | undefined,
): string {
  if (!node) return "";
  return (node.url || "").trim() || (node.relayUrl || "").trim();
}

const PREVIOUS_PALETTE = ["#7c6bd6", "#c9b84a", "#6a8dc2", "#c66a7a", "#8a8f99", "#7aae8a"] as const;
const OLD_PALETTE = ["#6d5bcf", "#0ea5a0", "#e07a2f", "#2f8f4e", "#d9466a", "#7a9a3a"] as const;
const paletteIndex = [PREVIOUS_PALETTE, OLD_PALETTE] as const;

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
export function deviceLocalNodeURL(): string {
  const base = localBaseURL();
  if (!base || !/^https?:\/\//i.test(base)) return "";
  return base;
}

export function makeSelfNode(): NodeConnection {
  const identity = getSelfNodeIdentity();
  return {
    id: getSelfNodeId(),
    name: getSelfNodeName(),
    url: deviceLocalNodeURL(),
    color: PALETTE[0],
    // 边界即 instance_id：共享表里这台机器的行可能挂着随机 uuid（pc 行）。
    // 带上身份，去重链才能把它们当成同一台机器合并。
    instanceId: identity ? identity.id : undefined,
    _syntheticSelf: true,
  };
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
    // 同一 origin 上已有条目时优先留 self 行：合成行的地址是按当前设备推导的，
    // 比另一台设备写进共享表的同 origin 地址更可信。
    if (isSelfNodeConnection(prev)) continue;
    if (isSelfNodeConnection(n)) seen.set(key, n);
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
    // 合成 self 行只是本机视角：地址与身份都已由 prim（控制面里的那条）代表，
    // 合并后既不能顶替 prim 的地址，也不再算合成行（否则整行会被从 PUT 里剔除）。
    if (node._syntheticSelf) {
      out[i] = { ...prim, relayUrl, remote: prim.remote || node.remote };
    } else if (!prim.url && node.url) {
      out[i] = { ...node, relayUrl, color: node.color || prim.color, remote: prim.remote || node.remote };
    } else {
      out[i] = { ...prim, relayUrl, remote: prim.remote || node.remote };
    }
  }
  return out;
}

export function normalizeRecord(item: any): NodeConnection | null {
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
    _syntheticSelf: item._syntheticSelf === true ? true : undefined,
  };
}

function dedupNodesByID(nodes: NodeConnection[]): NodeConnection[] {
  const seen = new Map<string, NodeConnection>();
  for (const node of nodes) {
    const id = String(node.id || "").trim();
    if (!id) continue;
    // 同 id 两条（例如旧 local 行映射后撞上已有的 home 行）保留 self 那条：
    // 它的 URL 是设备推导的，另一条的 URL 是别的设备写下的。
    if (!seen.has(id) || isSelfNodeConnection(node)) seen.set(id, node);
  }
  return Array.from(seen.values());
}

export function normalizeAndDedup(list: any[]): NodeConnection[] {
  let nodes = (Array.isArray(list) ? list : []).map(normalizeRecord).filter((x: any): x is NodeConnection => !!x);
  nodes = dedupNodesByOrigin(nodes as NodeConnection[]);
  nodes = dedupNodesByInstance(nodes as NodeConnection[]);
  nodes = dedupNodesByID(nodes as NodeConnection[]);
  if (!nodes.some(isSelfNodeConnection)) {
    // 合成行追加在末尾，不插到最前：共享表里已有的行（尤其是主控 home）因此保持
    // 服务端顺序；本机在共享表里已有行时（id 或 instanceId 命中）根本不会走到这里。
    nodes.push(makeSelfNode());
    // 合成行自带 instanceId，可能与刚学到 instanceId 的共享行是同一台机器：再跑一遍去重。
    nodes = dedupNodesByInstance(nodes as NodeConnection[]);
    nodes = dedupNodesByID(nodes as NodeConnection[]);
  }
  const self = nodes.find(isSelfNodeConnection);
  if (self) {
    // 合成行（控制面表里还没有这台机器）按当前设备推导地址；共享行保留它自己的
    // 直连网址（统一后的 pc 就是靠这条留住 /mindfs-win）。本机请求的寻址另走
    // deviceLocalNodeURL()（runtime.resolveNodeBaseURL），不靠改写表里的地址。
    if (!self.url) self.url = deviceLocalNodeURL();
    // 本机不是「另一台机器」。
    if (self.remote) self.remote = false;
  }
  return nodes;
}
