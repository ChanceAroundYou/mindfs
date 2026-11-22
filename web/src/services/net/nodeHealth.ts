/**
 * 节点可达性：最近一次请求里，谁失败了、为什么。
 *
 * 它只是**请求的副产物**，自己不发起任何探测 —— 面板里的「在线」按需探活，
 * 这里的「离线」意味着「刚才真的有一条请求打在它身上，没成功」。两者含义不同，
 * 不要合成一套。
 *
 * 为什么要集中：不可达此前是各用各的临时判断 —— 工作台的 unreachableNodes、
 * 多项目会话组的 `_unreachable`、节点面板的 probeStatus，三处各记一份、口径各异；
 * 换一个界面（文件树）就完全没有。集中后「这台机器现在连不连得上」只有一个答案。
 *
 * 与 `nodeRegistry` 一样是服务层的可变单例：视图不许直接 import（规则见
 * docs/agent/rules/frontend/layering.md），一律经 `hooks/useNodeHealth` 取用。
 */
export type NodeHealthStatus = "online" | "offline";

export type NodeHealth = {
  status: NodeHealthStatus;
  /** 失败原因（成功时为空）。给「为什么连不上」提供原文，不做文案映射。 */
  detail?: string;
  /** 观测时刻（毫秒）。 */
  at: number;
};

export type NodeHealthSnapshot = {
  /** 每次变更自增：useSyncExternalStore 靠它判断快照有没有换过。 */
  version: number;
  /** 按节点 id（`local` / 远端节点 id）索引。 */
  nodes: Record<string, NodeHealth>;
  /** 节点表（主节点上的 `nodes.json`）最近一次拉取的结果；null = 还没拉过。 */
  nodeTable: NodeHealth | null;
};

const listeners = new Set<() => void>();
const nodes = new Map<string, NodeHealth>();
let nodeTable: NodeHealth | null = null;
let snapshot: NodeHealthSnapshot = { version: 0, nodes: {}, nodeTable: null };

function rebuild(): void {
  snapshot = {
    version: snapshot.version + 1,
    nodes: Object.fromEntries(nodes),
    nodeTable,
  };
  for (const fn of listeners) {
    try {
      fn();
    } catch {
      // 某个订阅者自己炸了不该拖垮其他界面。
    }
  }
}

function normalizeId(nodeId: unknown): string {
  return String(nodeId ?? "").trim();
}

/** 记一次成功。未知节点也记：下次失败前它就是「在线过」。 */
export function markNodeReachable(nodeId: unknown, at: number = Date.now()): void {
  const id = normalizeId(nodeId);
  if (!id) return; // 聚合请求（不带 nodeId）不代表任何一台机器
  const prev = nodes.get(id);
  if (prev?.status === "online") {
    // 内容没变就别 bump 版本：一轮刷新里每台机器都会成功一次，
    // 每次都广播会让文件树/会话列表无谓重渲染。
    return;
  }
  nodes.set(id, { status: "online", at });
  rebuild();
}

export function markNodeUnreachable(nodeId: unknown, detail: unknown = "", at: number = Date.now()): void {
  const id = normalizeId(nodeId);
  if (!id) return;
  const nextDetail = String(detail ?? "").trim();
  const prev = nodes.get(id);
  if (prev?.status === "offline" && (prev.detail || "") === nextDetail) {
    return;
  }
  nodes.set(id, { status: "offline", detail: nextDetail, at });
  rebuild();
}

export function markNodeTableReachable(at: number = Date.now()): void {
  if (nodeTable?.status === "online") return;
  nodeTable = { status: "online", at };
  rebuild();
}

/**
 * 节点表拉不到。它与「某个节点不可达」是两件事：节点表在主节点上，
 * 拉不到意味着**屏幕上这份节点清单可能不是最新的**（可能少了刚添加的机器，
 * 也可能还留着已删除的），继续拿它当事实渲染就会把「不知道」说成「没有」。
 */
export function markNodeTableUnreachable(detail: unknown = "", at: number = Date.now()): void {
  const nextDetail = String(detail ?? "").trim();
  if (nodeTable?.status === "offline" && (nodeTable.detail || "") === nextDetail) return;
  nodeTable = { status: "offline", detail: nextDetail, at };
  rebuild();
}

export function nodeHealthSnapshot(): NodeHealthSnapshot {
  return snapshot;
}

export function subscribeNodeHealth(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/** 仅供测试与登出使用：把观测结果清空，别让上一个账户的失败留在界面上。 */
export function resetNodeHealth(): void {
  nodes.clear();
  nodeTable = null;
  rebuild();
}